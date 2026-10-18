// Package sigclient は配備済みのシグナリングサーバへ WebSocket でつなぐクライアントである。
//
// 役目は「部屋を作る・入る・任意の JSON を相手へ中継してもらう」の3つだけで、
// WebRTC のことも対戦のことも知らない。data は不透明に扱い、中身を解釈しない
// （サーバが解釈しないのと同じ）。
//
// サーバ側の型（internal/signaling）は再利用していない。あちらはサーバの都合で
// 変わりうるものであり、こちらが依存すると配布物にサーバのコードが混ざる。
// 線の上の綴りだけを合わせ、型は別々に持つ。
//
// **通知の到着順は保証されない。** 第2段階a §11 で実測されており、peer-joined より
// 先に signal が届くこともある。したがって Next は届いた順にそのまま渡すだけで、
// 並べ替えも「次に来るはず」の待ち合わせもしない。順序の判断は呼び出し側の責務である。
package sigclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/coder/websocket"
)

// ErrUnreadable は届いた1通を解釈できなかったことを表す。
//
// 未知の種別も壊れた JSON も網から来る以上ありうるので、これは接続の失敗ではない。
// 呼び出し側は errors.Is で見分けて、その1通を捨てて Next を続けてよい。
// 転送そのものの失敗（接続断）はこれを包まないので、そちらは対戦の中断にあたる。
var ErrUnreadable = errors.New("sigclient: 解釈できない通知")

// readLimit は1通の受信上限。サーバは data を 16 KiB で切るので、包みを足しても
// これに収まる。既定（32 KiB）でも足りるが、上限の根拠を明示するために置いている。
const readLimit = 64 << 10

// EventKind はサーバからの通知の種別。
type EventKind int

const (
	EventCreated EventKind = iota
	EventJoined
	EventPeerJoined
	EventSignal
	EventPeerLeft
	EventError
)

func (k EventKind) String() string {
	switch k {
	case EventCreated:
		return "created"
	case EventJoined:
		return "joined"
	case EventPeerJoined:
		return "peer-joined"
	case EventSignal:
		return "signal"
	case EventPeerLeft:
		return "peer-left"
	case EventError:
		return "error"
	}
	return fmt.Sprintf("EventKind(%d)", int(k))
}

// Event はサーバからの通知1つ。使うフィールドは種別によって異なる。
type Event struct {
	Kind EventKind
	Room string
	Data json.RawMessage
	Code string
}

// parseEvent はサーバからの1通を解釈する。
//
// WebSocket から切り離してあるのは、通信なしでテストできるようにするためである。
// data は不透明に扱う。サーバが中身を見ないのと同じく、こちらも素通しする。
func parseEvent(b []byte) (Event, error) {
	var raw struct {
		Type string          `json:"type"`
		Room string          `json:"room"`
		Data json.RawMessage `json:"data"`
		Code string          `json:"code"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return Event{}, fmt.Errorf("%w: JSON として読めない: %w", ErrUnreadable, err)
	}
	e := Event{Room: raw.Room, Data: raw.Data, Code: raw.Code}
	switch raw.Type {
	case "created":
		e.Kind = EventCreated
	case "joined":
		e.Kind = EventJoined
	case "peer-joined":
		e.Kind = EventPeerJoined
	case "signal":
		e.Kind = EventSignal
	case "peer-left":
		e.Kind = EventPeerLeft
	case "error":
		e.Kind = EventError
	default:
		return Event{}, fmt.Errorf("%w: 未知の種別 %q", ErrUnreadable, raw.Type)
	}
	return e, nil
}

// encodeRequest はクライアントからの要求1つを組み立てる。
//
// 綴りは配備済みサーバの受け口（第2段階a §3.1）に合わせてある。room と data は
// 空なら省く。サーバの Request 型と同じ形だが、あちらを import はしない。
func encodeRequest(action, room string, data any) ([]byte, error) {
	req := struct {
		Action string          `json:"action"`
		Room   string          `json:"room,omitempty"`
		Data   json.RawMessage `json:"data,omitempty"`
	}{Action: action, Room: room}
	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("sigclient: data を符号化できない: %w", err)
		}
		req.Data = raw
	}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("sigclient: 要求を符号化できない: %w", err)
	}
	return b, nil
}

// Client はシグナリングサーバへの1本の接続。
//
// read と write を関数として持つのは、解釈と組み立てを実接続から切り離して
// テストするためである。Dial はここに WebSocket を差し込む。
//
// **受信は専用の goroutine が回し、Next はその結果を受け取るだけである。**
// coder/websocket は Read/Write に渡した context が切れると、その1回の呼び出しを
// やめるのではなく**接続そのものを閉じる**（同ライブラリの仕様）。呼び出し側が
// 「この1通は3秒待って諦める」つもりで context に期限を付けると、接続ごと死ぬ。
// 期限付きの context を渡すのはごく自然な書き方なので、その罠を Client の外に
// 出さないため、実際の Read には接続の生存期間の context だけを渡し、
// 呼び出し側の context は「待つのをやめる」ためだけに使う。
type Client struct {
	conn  *websocket.Conn
	read  func(context.Context) ([]byte, error)
	write func(context.Context, []byte) error

	// ctx は接続の生存期間。Close と Dial の失敗でのみ切れる。
	ctx    context.Context
	cancel context.CancelFunc

	msgs chan []byte // 受信済みで未処理の通知
	// readErr は受信 goroutine が止まった理由。msgs を閉じる前に書くので、
	// 閉じたチャネルから ok=false を得た側は必ず書き込み後の値を読む。
	readErr error

	closeOnce sync.Once
	closeErr  error
}

// recvBuffer は受信済みで未処理の通知を溜める数。
//
// 1つの対戦で行き来する通知は SDP 2通と candidate 数十通で、しかも画面は
// 毎フレーム Next を呼ぶ。溢れることは想定していないが、溢れたら受信 goroutine が
// 待つだけで、取りこぼしにはならない。
const recvBuffer = 64

// newClientWith は読み書きの手を直に与えて Client を作る。テスト用。
func newClientWith(read func(context.Context) ([]byte, error), write func(context.Context, []byte) error) *Client {
	return start(&Client{read: read, write: write})
}

// start は受信 goroutine を回し始める。
func start(c *Client) *Client {
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.msgs = make(chan []byte, recvBuffer)
	go c.recvLoop()
	return c
}

// recvLoop は接続が切れるまで通知を読み続ける。
func (c *Client) recvLoop() {
	for {
		b, err := c.read(c.ctx)
		if err != nil {
			c.readErr = err
			close(c.msgs)
			return
		}
		select {
		case c.msgs <- b:
		case <-c.ctx.Done():
			c.readErr = c.ctx.Err()
			close(c.msgs)
			return
		}
	}
}

// Dial はシグナリングサーバへつなぐ。
func Dial(ctx context.Context, url string) (*Client, error) {
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("sigclient: 接続できない: %w", err)
	}
	conn.SetReadLimit(readLimit)
	return start(&Client{
		conn: conn,
		read: func(ctx context.Context) ([]byte, error) {
			// メッセージ種別は捨てる。サーバはテキストしか送らないし、
			// 仮にバイナリで来ても中身は JSON として解釈を試みればよい。
			_, b, err := conn.Read(ctx)
			return b, err
		},
		write: func(ctx context.Context, b []byte) error {
			return conn.Write(ctx, websocket.MessageText, b)
		},
	}), nil
}

// Create は部屋の作成を頼む。応答（created）は Next で受け取る。
//
// ここで待たないのは、待ち時間の表示が画面側の責務だからである。
func (c *Client) Create(ctx context.Context) error {
	return c.send(ctx, "create", "", nil)
}

// Join は部屋に入ることを頼む。応答（joined か error）は Next で受け取る。
func (c *Client) Join(ctx context.Context, room string) error {
	return c.send(ctx, "join", room, nil)
}

// SendSignal は相手への中継を頼む。data の中身は解釈しない。
func (c *Client) SendSignal(ctx context.Context, data any) error {
	return c.send(ctx, "signal", "", data)
}

// send は要求を1つ送る。
//
// 実際の書き込みには接続の生存期間の context を渡す。呼び出し側の context を
// そのまま渡すと、期限切れのときに coder/websocket が接続ごと閉じてしまうためである
// （Client の型注釈を参照）。呼び出し側の context は「送るのをやめる」判断にだけ使う。
func (c *Client) send(ctx context.Context, action, room string, data any) error {
	b, err := encodeRequest(action, room, data)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("sigclient: %s を送る前に中止された: %w", action, ctx.Err())
	default:
	}
	if err := c.write(c.ctx, b); err != nil {
		return fmt.Errorf("sigclient: %s を送れない: %w", action, err)
	}
	return nil
}

// Next は通知を1つ受け取る。届くまで待つ。
//
// 返るエラーは2種類ある。ErrUnreadable を包むものは「その1通が読めなかった」だけで
// 接続は生きており、呼び出し側は Next を続けてよい。それ以外は転送の失敗である。
// サーバからの error 通知はエラーではなく EventError として返る。あれは失敗の知らせ
// という**データ**であって、接続が壊れたことではない。
func (c *Client) Next(ctx context.Context) (Event, error) {
	// 届いている分を先に渡す。受信 goroutine が既に止まっていても、
	// 溜まっている通知を捨てずに配り切ってから理由を返す。
	select {
	case b, ok := <-c.msgs:
		if !ok {
			return Event{}, c.recvErr()
		}
		return parseEvent(b)
	default:
	}
	select {
	case b, ok := <-c.msgs:
		if !ok {
			return Event{}, c.recvErr()
		}
		return parseEvent(b)
	case <-ctx.Done():
		// 呼び出し側が待つのをやめただけである。接続は生きており、
		// 次の Next で続きを受け取れる。
		return Event{}, ctx.Err()
	}
}

// recvErr は受信 goroutine が止まった理由を返す。
func (c *Client) recvErr() error {
	if c.readErr == nil {
		return errors.New("sigclient: 受信が終了した")
	}
	return fmt.Errorf("sigclient: 受信できない: %w", c.readErr)
}

// Close は接続を閉じる。二度目以降は何もせず、一度目と同じ結果を返す。
//
// defer で閉じつつ明示的にも閉じる書き方は普通にあるので、二度目が
// 別のエラーを返すと、実際には何も問題が起きていないのに記録が汚れる。
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		if c.cancel != nil {
			c.cancel()
		}
		if c.conn != nil {
			c.closeErr = c.conn.Close(websocket.StatusNormalClosure, "")
		}
	})
	return c.closeErr
}
