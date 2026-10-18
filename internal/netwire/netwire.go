// Package netwire は本物の通信部品を組み立てて、対戦の接続を1つ作る配線層である。
//
// internal/sigclient（WebSocket）と internal/rtc（pion）を internal/netplay の
// インタフェース（Signaller・RTCPeer）へ嵌めるのが唯一の仕事である。
// netplay がこの結線を自分で持つと sigclient と rtc を、ひいては coder/websocket と
// pion を import することになり、「netplay は通信を知らない」という約束（仕様書 §3）が
// 壊れる。だから何でも import してよい層をここに1つだけ作り、そこへ寄せる。
//
// **Ebitengine には依存させない。** 画面なしで go test できることがこのパッケージを
// gfx から分けている理由である。接続の組み立ては罠が多い（ブロックする Dial、
// ブロックしてはならない Next、期限付き context が接続そのものを殺す送信）ので、
// ここが検証できなくなると誰も検証できなくなる。
package netwire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/morststs/inkmaru-web/internal/netplay"
	"github.com/morststs/inkmaru-web/internal/rtc"
	"github.com/morststs/inkmaru-web/internal/rules"
	"github.com/morststs/inkmaru-web/internal/sigclient"
	"github.com/morststs/inkmaru-web/internal/ui"
)

// maxUnreadableSkips は Next 1回で読み飛ばす「解釈できない通知」の上限。
//
// **上限は必須である。** 解釈できない通知が続くときに際限なく読み飛ばすと、
// 1フレームの中で回り続けて Update がブロックしたのと同じことになる。
// シグナリングは公開の経路で、こちらが制御できない相手も繋がる場所なので、
// 「解釈できない通知が延々と届く」は想定すべき状況である。
const maxUnreadableSkips = 64

// signalConn は netwire が sigclient.Client に求めるものだけを表す。
//
// 偽物に差し替えて、本物の WebSocket なしで配線を検証するために切ってある
// （配備済みのシグナリングサーバは課金対象で稼働中なので、テストは触れてはならない）。
type signalConn interface {
	Create(ctx context.Context) error
	Join(ctx context.Context, room string) error
	SendSignal(ctx context.Context, data any) error
	Next(ctx context.Context) (sigclient.Event, error)
	Close() error
}

// *sigclient.Client がそのまま満たすことをここで固定する。
var _ signalConn = (*sigclient.Client)(nil)

// dialResult は goroutine で行った Dial の結果。
type dialResult struct {
	conn signalConn
	err  error
}

// Handle は組み立て中／組み立て済みの接続1つ。画面はこれを毎フレーム Update する。
//
// **並行呼び出しは安全でない。** ゲームループ1本から呼ぶ前提である（Connector と同じ）。
// 別 goroutine が触るのは Dial の受け渡し（dialed と closed）だけで、そこだけを錠で守る。
type Handle struct {
	host bool
	// room はゲストが入ろうとしている部屋番号。ホストは created が届くまで空。
	room    string
	newPeer func(host bool) (netplay.RTCPeer, error)
	now     func() time.Time

	// dialed は Dial の結果を1つだけ運ぶ。容量1なので送り手は待たない。
	dialed chan dialResult
	// cancel は Dial を打ち切る。Close から呼ぶ。
	cancel context.CancelFunc

	// dialDone は Dial の結果を引き取ったか。
	dialDone bool
	// c は Dial が成功してから作る。それまでは nil で、状態は下の state が持つ。
	c *netplay.Connector

	// state と fail は Connector ができるまでの表示用。できたあとは Connector が正典。
	state netplay.ConnectState
	fail  string

	mu     sync.Mutex
	closed bool
}

// Start は接続の組み立てを非ブロッキングに始める。
//
// host が true なら部屋を作り、false なら room の部屋に参加する。
// 戻ったときにはまだ何も繋がっていない。画面は毎フレーム Update を呼び、
// State を見て表示を選ぶこと。
func Start(url string, host bool, room string) *Handle {
	dial := func(ctx context.Context) (signalConn, error) {
		return dialSignaling(ctx, url)
	}
	return startWith(dial, newRTCPeer, time.Now, host, room)
}

// dialSignaling はシグナリングサーバへ繋ぎ、結果を signalConn として返す。
//
// **失敗したときに明示的な nil を返すのが要点である。** sigclient.Dial は失敗時に
// (*sigclient.Client)(nil) を返すので、それをそのままインタフェースへ入れると
// 「型は入っているが値は nil」になり、インタフェースとしては nil ではなくなる。
// 呼び出し側（Dial 中に Close されたときの後始末）の nil 検査をすり抜けて
// nil レシーバの Close が呼ばれ、goroutine の中で panic してゲームごと落ちる。
func dialSignaling(ctx context.Context, url string) (signalConn, error) {
	c, err := sigclient.Dial(ctx, url)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// startWith は依存を差し替えられる形で組み立てを始める。テストはこちらを使う。
func startWith(
	dial func(context.Context) (signalConn, error),
	newPeer func(host bool) (netplay.RTCPeer, error),
	now func() time.Time,
	host bool,
	room string,
) *Handle {
	if now == nil {
		now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &Handle{
		host:    host,
		room:    room,
		newPeer: newPeer,
		now:     now,
		dialed:  make(chan dialResult, 1),
		cancel:  cancel,
	}
	// Dial が終わるまでの表示は Connector が始まったあとと同じにする。
	// 画面には既に「部屋を作成中…」「接続中…」があるので、新しい状態を増やさない。
	if host {
		h.state = netplay.StateCreating
	} else {
		h.state = netplay.StateConnecting
	}

	// **Dial はブロックする**（sigclient.Dial → websocket.Dial）ので Update から
	// 呼んではならない。goroutine 1本に閉じ込め、結果は容量1の緩衝で受け渡す。
	go func() {
		conn, err := dial(ctx)
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.closed {
			// **待っている間に Close された。** 遅れて届いた接続をここで閉じないと
			// WebSocket と受信 goroutine が居座る。Close 側の引き取りと同じ錠で
			// 判断するので、「閉じたのに誰も閉じない」隙間は無い。
			if conn != nil {
				conn.Close()
			}
			return
		}
		h.dialed <- dialResult{conn: conn, err: err}
	}()
	return h
}

// Update は組み立てを1フレームぶん進めて、今の状態を返す。**ブロックしない。**
func (h *Handle) Update() netplay.ConnectState {
	if h.isClosed() {
		return h.State()
	}
	if !h.dialDone {
		select {
		case r := <-h.dialed:
			h.adopt(r)
		default:
			// まだ繋がっていない。ここで待たない。
			return h.state
		}
	}
	if h.c == nil {
		// Dial に失敗した。終着点なので進むところは無い。
		return h.state
	}
	return h.c.Update()
}

// adopt は Dial の結果を引き取り、成功なら Connector を組み立てて始める。
func (h *Handle) adopt(r dialResult) {
	h.dialDone = true
	if r.err != nil {
		h.state = netplay.StateFailed
		h.fail = fmt.Sprintf("サーバに接続できませんでした (%v)", r.err)
		return
	}
	h.c = netplay.NewConnectorWith(&sigBridge{conn: r.conn}, h.newPeer, h.now)
	if h.host {
		h.c.StartHost()
	} else {
		h.c.StartGuest(h.room)
	}
}

// State は今の状態を返す。Update を呼ばずに参照したいときに使う。
func (h *Handle) State() netplay.ConnectState {
	if h.c != nil {
		return h.c.State()
	}
	return h.state
}

// UIPhase は接続の状態を、画面が扱う語彙（ui.ConnPhase）へ写す。
//
// **この変換がここにあるのは、ここが両方の型を見られる唯一の層だからである。**
// internal/netplay は ErrPeerGone・PeerFault のために internal/ui を import
// している（netopponent.go）ので、依存の向きは netplay → ui の一方向であり、
// ui 側から netplay.ConnectState を受け取ることはできない（import 環になる）。
// netwire は netplay も ui も import してよい層で、しかも画面と違って
// go test で検証できる。文言の対応表は ui.ConnectStatus が持ち、
// 状態の対応表はここが持つ。どちらもテストで固定してある。
//
// 数値が偶然一致していることに頼らず、1つずつ対応を書く（convertEvent と同じ理由）。
func UIPhase(st netplay.ConnectState) ui.ConnPhase {
	switch st {
	case netplay.StateCreating:
		return ui.ConnCreating
	case netplay.StateWaitingPeer:
		return ui.ConnWaitingPeer
	case netplay.StateConnecting:
		return ui.ConnConnecting
	case netplay.StateReady:
		return ui.ConnReady
	case netplay.StateFailed:
		return ui.ConnFailed
	default:
		return ui.ConnIdle
	}
}

// Phase は今の状態を画面の語彙で返す。画面はこれを ui.ConnectStatus に渡す。
func (h *Handle) Phase() ui.ConnPhase { return UIPhase(h.State()) }

// Room は部屋番号を返す。ホストは created が届くまで空である。
func (h *Handle) Room() string {
	if h.c != nil {
		return h.c.Room()
	}
	return h.room
}

// FailReason は諦めた理由を返す。画面にそのまま出せる日本語である。
func (h *Handle) FailReason() string {
	if h.c != nil {
		return h.c.FailReason()
	}
	return h.fail
}

// Opponent は開通した相手を返す。StateReady より前は nil。
func (h *Handle) Opponent() *netplay.NetOpponent {
	if h.c != nil {
		return h.c.Opponent()
	}
	return nil
}

// Seat は自分の席を返す。ホストが P1、ゲストが P2 である。
//
// Connector があるならそれを素通しする。Dial の途中はまだ Connector が無いが、
// 席は「部屋を作る側か入る側か」だけで決まっており接続の進み具合に依らないので、
// 同じ規則で答える。食い違うと盤面が鏡像になり全対戦が board-mismatch になる。
func (h *Handle) Seat() rules.Player {
	if h.c != nil {
		return h.c.Seat()
	}
	if h.host {
		return rules.P1
	}
	return rules.P2
}

// Close は組み立てをやめ、握っている資源をすべて手放す。二度呼んでもよい。
//
// **State は動かない。** 取り消しても表示は最後の状態のままなので、
// 画面側は「取り消した」ことを自分で憶えてタイトルへ戻ること。
func (h *Handle) Close() {
	h.mu.Lock()
	h.closed = true
	// Dial が先に終わっていた場合、結果は緩衝に残っている。引き取って閉じないと
	// 誰も閉じない。goroutine 側の後始末と同じ錠の下で見るので、
	// 「これから送られてくる分」を取り逃すことはない。
	select {
	case r := <-h.dialed:
		if r.conn != nil {
			r.conn.Close()
		}
	default:
	}
	h.mu.Unlock()

	h.cancel()
	if h.c != nil {
		h.c.Close()
	}
}

func (h *Handle) isClosed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}

// sigBridge は sigclient.Client を netplay.Signaller に見せる。
//
// 要点は2つある。**Next はブロックしてはならない**のに Client.Next は届くまで待つ形で
// あること、**Send に期限付きの context を渡してはならない**ことである。
type sigBridge struct {
	conn signalConn
	// dead は転送そのものが壊れたか。壊れたことは1回だけ知らせ、以後は黙る。
	dead bool
}

// Send は要求を1つ送る。ブロックしない。
//
// **context.Background() を渡すこと。** Client.send は書き込みの前に ctx.Done() を
// 見て即座に失敗し、Connector はその失敗を StateFailed に直結させる。フレームの
// 都合で短い期限を付けると、まだ生きている接続を1フレームの遅れだけで
// 「切れた」と断じることになる（第2段階 Task 7 で潰した罠）。
// Client の実際の書き込みは接続の生存期間の context で行われるので、
// ここで期限を付けなくても送りっぱなしにはならない。
func (s *sigBridge) Send(action string, room string, data any) error {
	ctx := context.Background()
	switch action {
	case "create":
		return s.conn.Create(ctx)
	case "join":
		return s.conn.Join(ctx, room)
	case "signal":
		return s.conn.SendSignal(ctx, data)
	}
	return fmt.Errorf("netwire: 未知の要求 %q", action)
}

// Next は届いている通知を1つ返す。**届いていなければ即座に false を返す。**
//
// 期限切れの context を渡すのが要点である。Client.Next は最初に
// select { case <-c.msgs: … default: } で届いている分を非ブロッキングに覗くので、
// 死んだ context でも溜まっている通知は取りこぼさない。空のときだけ2つ目の
// select に落ち、そこでは ctx.Done() が既に閉じているので即座に戻る。
// **待つ経路が存在しない。**
func (s *sigBridge) Next() (netplay.SigEvent, bool) {
	if s.dead {
		return netplay.SigEvent{}, false
	}
	for i := 0; i < maxUnreadableSkips; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		e, err := s.conn.Next(ctx)
		if err != nil {
			// **context のエラーは失敗ではない。** 「まだ届いていない」だけである。
			// ここを失敗扱いにすると、通知が1つも溜まっていない普通のフレームで
			// 全対戦が即座に落ちる。
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return netplay.SigEvent{}, false
			}
			// 解釈できない1通は捨てて次を見る。網から来る以上ありうることで、
			// 接続の失敗ではない。
			if errors.Is(err, sigclient.ErrUnreadable) {
				continue
			}
			// それ以外は転送そのものの死である。**1回だけ**合成して知らせる。
			// 黙って false を返し続けると、Connector は30秒（相手待ちなら10分）の
			// 締切まで原因の分からないまま待つことになる。
			s.dead = true
			return netplay.SigEvent{Kind: netplay.SigError, Code: "disconnected"}, true
		}
		ev, ok := convertEvent(e)
		if !ok {
			// 知らない種別。読めなかった1通と同じ扱いで、上限にも数える。
			continue
		}
		return ev, true
	}
	// 上限まで読み飛ばした。残りは次のフレームで見る。
	return netplay.SigEvent{}, false
}

func (s *sigBridge) Close() error { return s.conn.Close() }

// convertEvent は sigclient の通知を netplay の通知に写す。
//
// 種別は綴りを合わせてあるだけで、同じ型ではない（netplay は sigclient を
// import しない）。数値が偶然一致していることに頼らず、1つずつ対応を書く。
func convertEvent(e sigclient.Event) (netplay.SigEvent, bool) {
	out := netplay.SigEvent{Room: e.Room, Code: e.Code, Data: []byte(e.Data)}
	switch e.Kind {
	case sigclient.EventCreated:
		out.Kind = netplay.SigCreated
	case sigclient.EventJoined:
		out.Kind = netplay.SigJoined
	case sigclient.EventPeerJoined:
		out.Kind = netplay.SigPeerJoined
	case sigclient.EventSignal:
		out.Kind = netplay.SigSignal
	case sigclient.EventPeerLeft:
		out.Kind = netplay.SigPeerLeft
	case sigclient.EventError:
		out.Kind = netplay.SigError
	default:
		return netplay.SigEvent{}, false
	}
	return out, true
}

// peerRole はホストかどうかから WebRTC の役割を決める。
//
// **ホストが offer を作る側である。** 取り違えると双方が offer を作る（または
// 双方が answer を待つ）ことになり、ゲストの接続が全滅する。
func peerRole(host bool) rtc.Role {
	if host {
		return rtc.RoleOfferer
	}
	return rtc.RoleAnswerer
}

// newRTCPeer は本物の WebRTC 接続を1つ作り、netplay.RTCPeer に見せる。
func newRTCPeer(host bool) (netplay.RTCPeer, error) {
	p, err := rtc.NewPeer(peerRole(host))
	if err != nil {
		return nil, err
	}
	return &peerBridge{p: p}, nil
}

// peerBridge は rtc.Peer を netplay.RTCPeer に見せる。
//
// netplay は SDP と candidate を生の JSON バイト列で運ぶ（中身を解釈するのは rtc
// だけでよく、状態機械にとっては相手へそのまま運ぶ不透明な包みである）。
// rtc.Description / rtc.Candidate は json タグ付きの平叙な値なので、
// この覆いは json.Marshal / json.Unmarshal だけで済む。
type peerBridge struct{ p *rtc.Peer }

func (b *peerBridge) LocalDescription() ([]byte, error) {
	d, err := b.p.LocalDescription()
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("netwire: SDP を符号化できない: %w", err)
	}
	return raw, nil
}

// SetRemote は相手の SDP を設定する。
//
// **失敗はすべて netplay.ErrBadSignal で包む。この関数の引数は 100% 相手が
// 寄越したバイト列だからである。** 包まないと Connector はそれを接続の死として
// 扱い、版の食い違う相手が SDP を1バイト崩した1通を送るだけで対戦が落ちる。
//
// JSON と種別だけを見るのでは足りない。`{"type":"offer","sdp":"<ゴミ>"}` や
// `sdp` が空の1通は、JSON としても種別としても正しいまま pion へ届き、
// そこで初めて「SDP を読めない」「ice-ufrag が無い」という一般のエラーになる。
// 手で壊すなら真っ先に作る形がそれなので、ここで倒しておく。
//
// pion のエラーを種類で選り分ける道は採らない。相手起因の失敗（answer を先に
// 送りつける）が、こちらのピアの故障と同じ InvalidModificationError で返るため、
// 分類が目的と一致しない。
//
// 失うもの: こちらのピアが本当に故障していると、即座の理由ではなく30秒の締切での
// 「接続できませんでした」になる。引数が相手のバイト列である以上、これは
// applySignal の方針（1通で対戦を潰せるようにしてはならない）と整合する取引である。
func (b *peerBridge) SetRemote(sdp []byte) error {
	var d rtc.Description
	if err := json.Unmarshal(sdp, &d); err != nil {
		return fmt.Errorf("%w: 相手の SDP を読めない: %v", netplay.ErrBadSignal, err)
	}
	if d.Type != "offer" && d.Type != "answer" {
		return fmt.Errorf("%w: 知らない SDP の種別 %q", netplay.ErrBadSignal, d.Type)
	}
	if err := b.p.SetRemote(d); err != nil {
		return fmt.Errorf("%w: 相手の SDP を設定できない: %v", netplay.ErrBadSignal, err)
	}
	return nil
}

func (b *peerBridge) AddCandidate(cand []byte) error {
	var c rtc.Candidate
	if err := json.Unmarshal(cand, &c); err != nil {
		// 1つの candidate の失敗で諦めない。ICE は使えるものが1つあれば繋がる。
		// Connector は元から candidate の失敗を捨てるが、SDP と同じ番兵で包んで
		// 「相手の中身が悪いだけ」であることを型でも表しておく。
		return fmt.Errorf("%w: candidate を読めない: %v", netplay.ErrBadSignal, err)
	}
	return b.p.AddCandidate(c)
}

func (b *peerBridge) OnLocalCandidate(f func([]byte)) {
	if f == nil {
		b.p.OnLocalCandidate(nil)
		return
	}
	b.p.OnLocalCandidate(func(c rtc.Candidate) {
		raw, err := json.Marshal(c)
		if err != nil {
			// 自分の candidate が符号化できないのは起こりえないが、
			// 起きたところで送れる形が無い。この1つを落として続ける。
			return
		}
		f(raw)
	})
}

func (b *peerBridge) Transport(ctx context.Context) (netplay.Transport, error) {
	// rtc.Peer.Transport の戻り値が既に netplay.Transport なので無変換で通る。
	return b.p.Transport(ctx)
}

func (b *peerBridge) Close() error { return b.p.Close() }
