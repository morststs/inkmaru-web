package netplay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/morststs/inkmaru-web/internal/rules"
)

// ConnectState は接続の組み立てがどこまで進んだかを表す。
//
// 画面はこの値だけを見て表示を選ぶ。手順の途中経過（offer を送ったか、
// candidate が何通届いたか）は外に出さない。出すと画面がプロトコルを
// 知ることになり、WebRTC の都合が表示層へ漏れる。
type ConnectState int

const (
	// StateIdle は StartHost / StartGuest がまだ呼ばれていない状態。
	StateIdle ConnectState = iota
	// StateCreating は部屋の作成を頼んで応答を待っている状態。
	// **この表示を省いてはならない。** create の応答はコールド時に1.4秒を
	// 超える（第2段階a §11 の実測）ので、押した瞬間に番号が出る前提の画面は
	// 「反応していない」と誤解される。
	StateCreating
	// StateWaitingPeer は部屋ができて、相手の参加を待っている状態。
	StateWaitingPeer
	// StateConnecting は相手が居ることが分かり、P2P の確立を進めている状態。
	StateConnecting
	// StateReady は DataChannel が開いて対戦を始められる状態。終着点である。
	StateReady
	// StateFailed は組み立てを諦めた状態。理由は FailReason が持つ。終着点である。
	StateFailed
)

func (s ConnectState) String() string {
	switch s {
	case StateIdle:
		return "Idle"
	case StateCreating:
		return "Creating"
	case StateWaitingPeer:
		return "WaitingPeer"
	case StateConnecting:
		return "Connecting"
	case StateReady:
		return "Ready"
	case StateFailed:
		return "Failed"
	}
	return fmt.Sprintf("ConnectState(%d)", int(s))
}

// ConnectTimeout は P2P の確立を諦めるまでの時間（仕様書 §7.4）。
//
// TURN を使わないので Symmetric NAT や一部の携帯回線では原理的に繋がらない。
// 繋がらないものを待ち続けても状況は変わらないので、明示的に諦めて
// CPU 対戦へ戻る導線を出す。部屋の作成（StateCreating）にも同じ上限を課す。
const ConnectTimeout = 30 * time.Second

// WaitPeerTimeout は相手の参加を待ち続ける上限。
//
// **ここに ConnectTimeout を課してはならない。** 部屋番号は口頭やチャットで
// 人が伝え、相手は画面上のキーパッドで6文字を打つ。30秒で切ると
// 「部屋を作る」機能そのものが使い物にならなくなる。仕様書 §7.4 の30秒は
// 「peer-joined のあと P2P の確立まで」であって（§8.4）、相手待ちではない。
//
// 値はサーバ側の部屋の寿命に合わせてある（signaling.DefaultRoomTTL = 10分）。
// それを過ぎると相手が正しい番号を打っても room-not-found になるので、
// 待ち続けても意味がない。
const WaitPeerTimeout = 10 * time.Minute

// maxEventsPerFrame は1フレームで処理する通知の上限。
//
// Update はゲームループから毎フレーム呼ばれるので、届いている分を
// 際限なく処理すると1フレームの仕事量がサーバ次第になる。
// 正常な対戦で1フレームに届くのは多くて数通である。
const maxEventsPerFrame = 64

// SigKind はシグナリングサーバからの通知の種別。
//
// internal/sigclient の EventKind とは別に持つ。あちらを使うと netplay が
// sigclient を、ひいては coder/websocket を import することになり、
// 「netplay は通信を知らない」という約束（仕様書 §3）が壊れる。
// 綴りだけを合わせ、変換は配線層（gfx）が行う。
type SigKind int

const (
	SigCreated SigKind = iota
	SigJoined
	SigPeerJoined
	SigSignal
	SigPeerLeft
	SigError
)

func (k SigKind) String() string {
	switch k {
	case SigCreated:
		return "created"
	case SigJoined:
		return "joined"
	case SigPeerJoined:
		return "peer-joined"
	case SigSignal:
		return "signal"
	case SigPeerLeft:
		return "peer-left"
	case SigError:
		return "error"
	}
	return fmt.Sprintf("SigKind(%d)", int(k))
}

// SigEvent はシグナリングサーバからの通知1つ。使う欄は種別で異なる。
// Data は signal のときだけ意味を持ち、中身は signalEnvelope の JSON である。
type SigEvent struct {
	Kind SigKind
	Room string
	Data []byte
	Code string
}

// Signaller は部屋の作成・参加と、相手への中継を頼む先。
//
// **Next はブロックしてはならない。** 届いていなければ false を返して即座に戻ること。
// Update はゲームループの中から呼ばれるので、ここで待つと画面が止まる。
// internal/sigclient の Next は届くまで待つ形なので、配線層で
// 「期限切れの context を渡し、context のエラーなら false」に変換すること。
//
// **Send もブロックしてはならない。** Update は sendPending・sendLocalSDP・
// flushLocalCandidates（毎フレーム）から Send を呼ぶので、ここで待つと
// Next と同じく画面が止まる。送り出しを待つ必要があるなら実装側で緩衝すること。
//
// **Send に期限付きの context を渡してはならない。** internal/sigclient の send は
// 書き込みの前に ctx.Done() を見て即座に失敗し、Connector はその失敗を
// StateFailed に直結させる（sendPending・sendSignal）。フレームの都合で短い期限を
// 付けると、まだ生きている接続を1フレームの遅れだけで「切れた」と断じることになる。
//
// Send の data は JSON に符号化できる値である（nil なら省く）。
type Signaller interface {
	Send(action string, room string, data any) error
	Next() (SigEvent, bool)
	Close() error
}

// ErrBadSignal は相手から届いた1通を解釈できなかったことを表す。
//
// **RTCPeer の実装は、相手が送ってきた中身が悪いだけのときこれを包むこと。**
// シグナリングはこちらが制御できない相手から届く場所なので、SetRemote の失敗を
// すべて接続の死として扱うと、版の食い違う相手が壊れた SDP を1通送るだけで
// 対戦を潰せる。ピア自体の故障（記述を設定できない・資源が無い）とは区別し、
// これを包んだ失敗はその1通だけを捨てる。candidate は元から1つ捨てるだけで
// 済ませており（applySignal）、SDP だけがその方針から外れていた。
var ErrBadSignal = errors.New("netplay: 解釈できない signal")

// RTCPeer は1つの相手との WebRTC 接続。
//
// **SDP と candidate を生の JSON バイト列で受け渡すのが要点である。**
// internal/rtc の Description / Candidate をここで名指すと netplay が rtc を、
// ひいては pion を import することになる。中身を解釈するのは rtc 側だけでよく、
// 状態機械にとっては「相手へそのまま運ぶ不透明な包み」でしかない。
// *rtc.Peer との変換（Description/Candidate ⇄ JSON）は配線層の薄い覆いが行う。
//
// Transport は開いていなければ即座に context のエラーを返すこと。Connector は
// 毎フレーム「すでに切れた context」を渡して開通を確かめる。
type RTCPeer interface {
	// LocalDescription は自分の SDP（offer か answer）を作り、自分に設定して返す。
	LocalDescription() ([]byte, error)
	// SetRemote は相手の SDP を設定する。
	// 相手が送ってきた中身が悪いだけのときは ErrBadSignal を包むこと。
	// そうしない失敗は接続の死として扱われる。
	SetRemote(sdp []byte) error
	// AddCandidate は相手の ICE candidate を1つ受け取る。
	// 遠隔記述より先に届いた分の緩衝は実装側の責任である（rtc.Peer が行う）。
	AddCandidate(cand []byte) error
	// OnLocalCandidate は自分の candidate の届け先を決める。
	// 別の goroutine から呼ばれうる。
	OnLocalCandidate(f func([]byte))
	// Transport は DataChannel が開いていれば経路を返す。
	Transport(ctx context.Context) (Transport, error)
	Close() error
}

// signal の包み。中身（Body）は不透明に運ぶ。
const (
	signalSDP = "sdp"
	signalICE = "ice"
)

type signalEnvelope struct {
	Kind string          `json:"kind"`
	Body json.RawMessage `json:"body"`
}

// pendingReq は「次の Update で送る要求」。
type pendingReq int

const (
	pendingNone pendingReq = iota
	pendingCreate
	pendingJoin
)

// Connector はシグナリングと WebRTC の手順を1つの状態機械にまとめる。
//
// 画面は StartHost / StartGuest を1回呼び、あとは毎フレーム Update を呼ぶだけでよい。
// **Update はブロックしない。** 送信も受信も待たず、進めるところまで進めて戻る。
//
// **並行呼び出しは安全でない。** ゲームループ1本から呼ぶ前提である。
// 別 goroutine から来るのは RTCPeer の candidate 通知だけで、それは
// 錠付きの控えに積んで Update が取り出す。
type Connector struct {
	sig     Signaller
	newPeer func(host bool) (RTCPeer, error)
	now     func() time.Time

	state ConnectState
	// host は自分が部屋を作った側か。席と offer/answer の役割の両方を決める。
	host    bool
	room    string
	fail    string
	pending pendingReq

	peer RTCPeer
	opp  *NetOpponent

	remoteSDP bool // 相手の SDP を設定済み（2通目は捨てる）
	sentSDP   bool // 自分の SDP を送信済み

	// deadline は今の状態を諦める時刻。ゼロ値なら諦めない。
	deadline time.Time

	closed bool
	// released は資源（opp・peer・sig）を解放したか。failf と Close の
	// 両方から解放するので、実際に閉じるのは一度きりにする。
	released bool

	// mu は localCands だけを守る。candidate の通知は pion の goroutine から来る。
	mu         sync.Mutex
	localCands [][]byte
}

// NewConnectorWith はシグナリングと WebRTC を差し替えられる形で Connector を作る。
//
// 実装を繋いだ版（WebSocket と pion）は配線層が用意する。ここで繋ぐと
// netplay が sigclient と rtc を import することになり、通信なしで走る
// テスト一式（仕様書 §10.1）が成り立たなくなる。
//
// newPeer は host（＝offer を作る側）かどうかを受け取る。now が nil なら time.Now。
func NewConnectorWith(s Signaller, newPeer func(host bool) (RTCPeer, error), now func() time.Time) *Connector {
	if now == nil {
		now = time.Now
	}
	return &Connector{sig: s, newPeer: newPeer, now: now, state: StateIdle}
}

// StartHost は部屋を作る側として始める。席は rules.P1、役割は offer を作る側。
//
// ここでは送らない。実際の送信は次の Update で行う。送信は網に触れる仕事なので、
// 入力の処理中ではなく毎フレームの1か所に集める。
func (c *Connector) StartHost() {
	if c.state != StateIdle {
		return
	}
	c.host = true
	c.pending = pendingCreate
	c.setState(StateCreating)
}

// StartGuest は部屋に入る側として始める。席は rules.P2、役割は answer を返す側。
func (c *Connector) StartGuest(room string) {
	if c.state != StateIdle {
		return
	}
	c.host = false
	c.room = room
	c.pending = pendingJoin
	c.setState(StateConnecting)
}

// Update は状態を1フレームぶん進めて、今の状態を返す。ブロックしない。
func (c *Connector) Update() ConnectState {
	if c.closed || c.state == StateIdle || c.state == StateFailed {
		return c.state
	}
	// 要求を送った回はそれで終える。応答が同じフレームで届くことはない
	// （最短でも往復1回ぶんかかる）ので、先へ進もうとしても得るものが無い。
	if c.pending != pendingNone {
		c.sendPending()
		return c.state
	}

	c.flushLocalCandidates()
	for i := 0; i < maxEventsPerFrame; i++ {
		e, ok := c.sig.Next()
		if !ok {
			break
		}
		c.handle(e)
		if c.state == StateFailed {
			return c.state
		}
	}
	if c.state == StateConnecting {
		c.pollTransport()
	}
	c.checkDeadline()
	return c.state
}

// State は今の状態を返す。Update を呼ばずに参照したいときに使う。
func (c *Connector) State() ConnectState { return c.state }

// Room は部屋番号を返す。ホストは created を受け取るまで空である。
func (c *Connector) Room() string { return c.room }

// FailReason は諦めた理由を返す。画面にそのまま出せる日本語で、
// 元のエラーコードを括弧で添えてある。
func (c *Connector) FailReason() string { return c.fail }

// Opponent は開通した相手を返す。StateReady より前は nil。
func (c *Connector) Opponent() *NetOpponent { return c.opp }

// Seat は自分の席を返す。**ホストが P1、ゲストが P2 である。**
//
// 両者がこの規則で同じ結論に達することが要点である。食い違うと互いの盤面が
// 鏡像になり、第1ターンの盤面ハッシュが必ず不一致になって（board-mismatch）
// 双方が「自分の勝ち」と表示する。役割はシグナリングが一意に決めるので、
// これを席の根拠にできる。StartHost / StartGuest より前の値に意味は無い。
func (c *Connector) Seat() rules.Player {
	if c.host {
		return rules.P1
	}
	return rules.P2
}

// Close はシグナリングと WebRTC の両方を閉じる。二度呼んでもよい。
//
// **Ready に達したあとに呼ぶと対戦の経路ごと切る。** 画面は対戦が終わってから
// 呼ぶこと。Ready より前で呼べば、組み立て途中の資源をすべて片付ける。
func (c *Connector) Close() {
	if c.closed {
		return
	}
	c.closed = true
	c.release()
}

// release は握っている資源をすべて手放す。二度目以降は何もしない。
//
// Close だけでなく failf からも呼ぶ。**失敗は終着点なので、そこで解放しないと
// 本番では pion の ICE agent と goroutine 群・受信 goroutine・開いた
// WebSocket が居座る。** 長寿命のブラウザタブでは、繋がらない相手に
// 何度か挑むだけで漏れが積み上がる。「失敗したら画面が Close を呼ぶ」規約に
// しないのは、忘れたときに静かに漏れるからである。
//
// **状態と理由には触れない。** State と FailReason は解放後も読める。
// 画面は失敗の理由をここから読み出して出す。
func (c *Connector) release() {
	if c.released {
		return
	}
	c.released = true
	if c.opp != nil {
		c.opp.Close()
	}
	if c.peer != nil {
		c.peer.Close()
	}
	c.sig.Close()
	// 送り損ねた candidate は捨てる。もう送る先が無い。
	c.mu.Lock()
	c.localCands = nil
	c.mu.Unlock()
}

// sendPending は溜めていた要求を送る。
func (c *Connector) sendPending() {
	req := c.pending
	c.pending = pendingNone
	switch req {
	case pendingCreate:
		if err := c.sig.Send("create", "", nil); err != nil {
			c.failf("部屋を作れませんでした (%v)", err)
		}
	case pendingJoin:
		if err := c.sig.Send("join", c.room, nil); err != nil {
			c.failf("部屋に入れませんでした (%v)", err)
		}
	}
}

// handle は通知を1つ処理する。
//
// **到着順を前提にしない。** 第2段階a §11 の実測により、signal 1通ごとに
// 別の Lambda 実行が起きるため送信順と到着順は一致せず、peer-joined より
// 先に signal が届くことがある。したがって「相手が居る」ことを知らせる通知は
// peer-joined だけでなく signal も含むものとして扱い（peerPresent）、
// created は届いた時点で部屋番号を記録するだけで状態を巻き戻さない。
func (c *Connector) handle(e SigEvent) {
	switch e.Kind {
	case SigCreated:
		// **最初の1通だけを採る。** 本番のサーバは接続ごとに1通しか送らないが、
		// 2通目で上書きすると、プレイヤーが既に相手へ読み上げた部屋番号が
		// 黙って別の番号に変わり、相手はいつまでも入れない。
		if e.Room != "" && c.room == "" {
			c.room = e.Room
		}
		if c.state == StateCreating {
			c.setState(StateWaitingPeer)
		}
	case SigJoined:
		// ゲストの参加が認められた。相手（ホスト）は既に部屋に居るが、
		// offer を作るのは相手なので、こちらはその到着を待つだけである。
		if e.Room != "" {
			c.room = e.Room
		}
	case SigPeerJoined:
		c.peerPresent()
	case SigSignal:
		c.peerPresent()
		c.applySignal(e.Data)
	case SigPeerLeft:
		if c.state != StateReady {
			c.failf("相手が退出しました (peer-left)")
		}
	case SigError:
		if c.state != StateReady {
			c.failf("%s", errorReason(e.Code))
		}
	}
}

// errorReason はサーバのエラーコードを画面に出せる日本語にする。
// 元のコードも残す。問い合わせに答えるにはコードが要る。
func errorReason(code string) string {
	switch code {
	case "room-not-found":
		return "その部屋番号は見つかりません (room-not-found)"
	case "room-full":
		return "その部屋は満員です (room-full)"
	case "disconnected":
		// サーバが送ってくるコードではなく、配線層が転送の死に対して合成するもの
		// （Signaller の注釈を参照）。既定の「サーバがエラーを返しました」だと
		// 切断が「サーバの都合で断られた」に読め、原因の見当が正反対に付く。
		return "サーバとの接続が切れました (disconnected)"
	case "":
		return "サーバがエラーを返しました"
	default:
		return "サーバがエラーを返しました (" + code + ")"
	}
}

// peerPresent は相手が部屋に居ると分かったときの処理。
//
// peer-joined でも signal でも呼ばれる。どちらが先に届くか保証されないため、
// 「先に来たほう」で接続を始められるようにしてある。二度目以降は
// ensurePeer と sentSDP が畳む。
func (c *Connector) peerPresent() {
	if c.state == StateReady || c.state == StateFailed {
		return
	}
	if !c.ensurePeer() {
		return
	}
	if c.state == StateCreating || c.state == StateWaitingPeer {
		c.setState(StateConnecting)
	}
	if c.host && !c.sentSDP {
		c.sendLocalSDP()
	}
}

// ensurePeer は Peer をまだ作っていなければ作る。作れたら true。
func (c *Connector) ensurePeer() bool {
	if c.peer != nil {
		return true
	}
	p, err := c.newPeer(c.host)
	if err != nil {
		c.failf("接続を用意できませんでした (%v)", err)
		return false
	}
	c.peer = p
	// candidate は pion の goroutine から来る。ここで送らずに控えへ積み、
	// Update が取り出して送る。網に触れる仕事をゲームループ1本に集めるためである。
	p.OnLocalCandidate(func(b []byte) {
		cp := append([]byte(nil), b...)
		c.mu.Lock()
		c.localCands = append(c.localCands, cp)
		c.mu.Unlock()
	})
	return true
}

// sendLocalSDP は自分の SDP を作って送る。ホストは offer、ゲストは answer。
func (c *Connector) sendLocalSDP() {
	d, err := c.peer.LocalDescription()
	if err != nil {
		c.failf("SDP を作れませんでした (%v)", err)
		return
	}
	if !c.sendSignal(signalSDP, d) {
		return
	}
	c.sentSDP = true
}

// applySignal は相手から中継された1通を Peer へ渡す。
//
// 解釈できない1通は捨てる。ここは公開のシグナリング経由で、こちらが制御できない
// 相手から届く場所である。1通で対戦を潰せるようにしてはならない。
func (c *Connector) applySignal(data []byte) {
	if c.peer == nil {
		return
	}
	var env signalEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return
	}
	switch env.Kind {
	case signalSDP:
		if c.remoteSDP {
			// 2通目の SDP は捨てる。設定し直すと状態が壊れる。
			return
		}
		if err := c.peer.SetRemote(env.Body); err != nil {
			if errors.Is(err, ErrBadSignal) {
				// 中身が悪いだけの1通は捨てる。candidate と同じ扱いである。
				// remoteSDP は立てない。正しい SDP があとから届けば受け取れる。
				return
			}
			c.failf("相手の SDP を設定できませんでした (%v)", err)
			return
		}
		c.remoteSDP = true
		// ゲストは offer を受け取って初めて answer を作れる。
		if !c.host && !c.sentSDP {
			c.sendLocalSDP()
		}
	case signalICE:
		// 1つの candidate の失敗で諦めない。ICE は使えるものが1つあれば繋がる。
		_ = c.peer.AddCandidate(env.Body)
	}
}

// flushLocalCandidates は溜まった自分の candidate を送る。
func (c *Connector) flushLocalCandidates() {
	c.mu.Lock()
	cands := c.localCands
	c.localCands = nil
	c.mu.Unlock()
	for _, b := range cands {
		if !c.sendSignal(signalICE, b) {
			return
		}
	}
}

// sendSignal は不透明な包みを1つ中継してもらう。送れたら true。
func (c *Connector) sendSignal(kind string, body []byte) bool {
	err := c.sig.Send("signal", "", signalEnvelope{Kind: kind, Body: json.RawMessage(body)})
	if err != nil {
		c.failf("接続情報を送れませんでした (%v)", err)
		return false
	}
	return true
}

// pollTransport は DataChannel が開いたかを確かめる。開いていれば Ready へ移る。
func (c *Connector) pollTransport() {
	if c.peer == nil {
		return
	}
	// すでに切れた context を渡す。開いていなければ即座に戻ってくる。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tr, err := c.peer.Transport(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			// まだ開いていないだけ。打ち切りは checkDeadline が判断する。
			return
		}
		c.failf("接続できませんでした (%v)", err)
		return
	}
	c.opp = NewNetOpponent(tr, c.now)
	// **ここで時計を起こす。** 呼び忘れると最初のターンの30秒が
	// 「初めて Deadline を見た時刻」から数え始まり、静かに延びる。
	c.opp.Start()
	c.setState(StateReady)
}

// setState は状態を移し、その状態の打ち切り時刻を決め直す。
func (c *Connector) setState(s ConnectState) {
	c.state = s
	switch s {
	case StateCreating, StateConnecting:
		c.deadline = c.now().Add(ConnectTimeout)
	case StateWaitingPeer:
		c.deadline = c.now().Add(WaitPeerTimeout)
	default:
		c.deadline = time.Time{}
	}
}

// checkDeadline は今の状態が長引きすぎていたら諦める。
func (c *Connector) checkDeadline() {
	if c.deadline.IsZero() || !c.now().After(c.deadline) {
		return
	}
	switch c.state {
	case StateCreating:
		c.failf("部屋を作れませんでした（サーバから応答がありません）")
	case StateWaitingPeer:
		c.failf("相手が参加しないまま部屋の期限が切れました")
	default:
		// **見出しの繰り返しにしない。** 画面は見出しに「接続できませんでした」を
		// 出し、その下にこの文を並べる（ui.ConnectStatus）。同じ文だと、同じことが
		// 2回書いてあるだけの画面になる。
		//
		// TURN を入れないと決めたので（仕様書 §7.4）、繋がらない相手とは諦めるのが
		// 恒久的な答えである。だからここは「なぜ起きたのか」と「次に何ができるのか」
		// を担う。
		// 秒数は日本語で埋める。%v に time.Duration をそのまま渡すと「30s」になり、
		// 折り返しの都合で行頭に「30s」だけが残ることがある。
		c.failf("%d秒待っても繋がりませんでした。回線によっては繋がらないことがあります。CPU対戦は遊べます", int(ConnectTimeout.Seconds()))
	}
}

// failf は理由を記録して StateFailed へ移す。最初の理由を残す。
func (c *Connector) failf(format string, args ...any) {
	if c.state == StateFailed {
		return
	}
	c.fail = fmt.Sprintf(format, args...)
	c.setState(StateFailed)
	c.release()
}
