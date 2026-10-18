// Package rtc は pion/webrtc を包み、netplay に「バイト列を送受信する1本の経路」だけを見せる。
//
// この層の役目は ICE を外へ漏らさないことである。特に ICE candidate の到着順は
// シグナリング経由では保たれない（第2段階a §11 の実測）ため、遠隔記述より先に届いた
// candidate を Peer の中で緩衝する。呼び出し側は順序を気にしなくてよい。
package rtc

import (
	"context"
	"fmt"
	"sync"

	"github.com/pion/webrtc/v4"

	"github.com/morststs/inkmaru-web/internal/netplay"
)

// Role は接続の役割。どちらが offer を作るかだけを表す。
type Role int

const (
	// RoleOfferer は offer を作り、DataChannel を開く側。
	RoleOfferer Role = iota
	// RoleAnswerer は offer を受けて answer を返し、DataChannel を受け取る側。
	RoleAnswerer
)

// dataChannelLabel は両者で一致していなければならない DataChannel の名前。
const dataChannelLabel = "inkmaru"

// maxPendingCandidates は遠隔記述が入るまでに溜める相手の candidate の上限。
//
// 現実の ICE 交換で出る candidate は「インタフェース数 × 種別 × 成分数」で、
// 多重ホームの端末でも十数件、多く見積もっても数十件である。256 はその一桁上で、
// まともな対戦が触れることはない。一方で上限としては十分低く、
// 1件数百バイトとして最悪でも 100KB 程度に収まる。
const maxPendingCandidates = 256

// Peer は1つの相手との接続。ICE candidate の緩衝をここに閉じ込める。
type Peer struct {
	pc   *webrtc.PeerConnection
	role Role

	mu        sync.Mutex
	remoteSet bool
	pending   []Candidate
	// pendingSeen は pending に入っているものの集合。同じ candidate を何度も
	// 送りつけて緩衝を埋める手を、畳んで無力化するために持つ。
	pendingSeen map[candidateKey]struct{}
	// onLocal は自分の candidate を外へ渡す手。登録前に出た分は localPending に溜める。
	onLocal      func(Candidate)
	localPending []Candidate
	tr           *dcTransport

	// rejected は解釈できずに捨てた相手の candidate の数。観測用で、外へは出さない。
	rejected int

	ready chan struct{}

	gone      chan struct{}
	goneOnce  sync.Once
	readyOnce sync.Once
	closeOnce sync.Once
}

// NewPeer は接続を1つ作る。役割に応じて DataChannel を開くか、受け取るかが決まる。
func NewPeer(role Role) (*Peer, error) {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("rtc: PeerConnection を作れない: %w", err)
	}

	p := &Peer{
		pc:    pc,
		role:  role,
		ready: make(chan struct{}),
		gone:  make(chan struct{}),
	}

	// 自分の candidate は出た端から外へ渡す（trickle）。
	// 溜めてから一括で送ると、相手の接続開始がその分だけ遅れる。
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			// 収集完了の合図。送るものは無い。
			return
		}
		ci := candidateFromPion(c.ToJSON())
		p.mu.Lock()
		f := p.onLocal
		if f == nil {
			p.localPending = append(p.localPending, ci)
			p.mu.Unlock()
			return
		}
		p.mu.Unlock()
		f(ci)
	})

	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		switch s {
		case webrtc.PeerConnectionStateFailed,
			webrtc.PeerConnectionStateClosed,
			webrtc.PeerConnectionStateDisconnected:
			p.markGone()
		}
	})

	switch role {
	case RoleOfferer:
		dc, err := pc.CreateDataChannel(dataChannelLabel, nil)
		if err != nil {
			pc.Close()
			return nil, fmt.Errorf("rtc: DataChannel を開けない: %w", err)
		}
		p.watchDataChannel(dc)
	case RoleAnswerer:
		pc.OnDataChannel(func(dc *webrtc.DataChannel) {
			p.watchDataChannel(dc)
		})
	}
	return p, nil
}

// watchDataChannel は DataChannel を受け取り、受信の受け皿をその場で用意する。
//
// OnMessage を Transport() まで遅らせてはならない。pion は DataChannel が開いた時点で
// 読み取りを回し始め、OnMessage が未登録のメッセージは黙って捨てる
// （webrtc の onMessage は handler が nil なら何もせず戻る）。相手が先に送って
// こちらが1フレーム遅れただけで第1ターンの commit が消え、commit-reveal は
// 揃わないまま無言で固まる。しかも症状は「相手の時間切れ」に見え、原因が追えない。
//
// ここでの登録は開く前に終わる。呼び出し元は offerer なら CreateDataChannel の直後、
// answerer なら OnDataChannel の中であり、pion は OnDataChannel の処理が終わるのを
// 待ってから handleOpen（＝読み取りの開始）へ進む。
//
// 見るのは最初の1本だけである。2本目にも受信を仕掛けると、誰も持っていない
// Transport にメッセージが流れ込み、さらに OnClose が貼り替わって
// 「閉じるのは2本目なのに死ぬのは1本目」という筋の通らない結びつきができる。
// ラベルを1本に固定してある今は 2本目は来ないが、ラベルを増やす人のために
// 罠を残さない。
func (p *Peer) watchDataChannel(dc *webrtc.DataChannel) {
	p.mu.Lock()
	if p.tr != nil {
		p.mu.Unlock()
		return
	}
	t := newDCTransport(dc)
	p.tr = t
	p.mu.Unlock()

	// OnClose はハンドラを1つしか持たない（代入であって追加ではない）ので、
	// Peer 側と Transport 側の両方の始末をここで一度に行う。
	// DataChannel の閉塞は SCTP の即時通知で速く、ICE の切断判定より何倍も早い。
	dc.OnClose(func() {
		t.markGone()
		p.markGone()
	})
	// 開いて初めて Send できる。Transport() が待つのはこの合図である。
	dc.OnOpen(func() { p.readyOnce.Do(func() { close(p.ready) }) })
}

// OnLocalCandidate は自分の candidate の届け先を決める。
// 登録より前に出ていた分もここでまとめて渡す。取りこぼすと繋がらないことがある。
func (p *Peer) OnLocalCandidate(f func(Candidate)) {
	p.mu.Lock()
	p.onLocal = f
	if f == nil {
		// 届け先を外すだけ。ここで取り出すと、次に登録した相手へ渡せなくなる。
		p.mu.Unlock()
		return
	}
	buffered := p.localPending
	p.localPending = nil
	p.mu.Unlock()

	for _, ci := range buffered {
		f(ci)
	}
}

// LocalDescription は役割に応じて offer か answer を作り、自分に設定してから返す。
//
// 返すのは作った時点の SDP であって、pc.LocalDescription() ではない。
// pc.LocalDescription() はその時点までに集まった candidate を SDP に混ぜ込むため、
// trickle で送る分と二重になり、どちらの経路で繋がったのかが分からなくなる。
func (p *Peer) LocalDescription() (Description, error) {
	var (
		sd  webrtc.SessionDescription
		err error
	)
	switch p.role {
	case RoleOfferer:
		sd, err = p.pc.CreateOffer(nil)
	case RoleAnswerer:
		sd, err = p.pc.CreateAnswer(nil)
	default:
		return Description{}, fmt.Errorf("rtc: 未知の役割 %d", p.role)
	}
	if err != nil {
		return Description{}, fmt.Errorf("rtc: SDP を作れない: %w", err)
	}
	if err := p.pc.SetLocalDescription(sd); err != nil {
		return Description{}, fmt.Errorf("rtc: 自分の SDP を設定できない: %w", err)
	}
	return descriptionFromPion(sd), nil
}

// AddCandidate は相手の candidate を受け取る。
//
// 遠隔記述がまだ入っていなければ溜める。第2段階a §11 の実測により、
// シグナリング経由の到着順は保たれないため、candidate が SDP より先に届くのは
// 例外ではなく通常の状況である。ここで捨てると「たまに繋がらない」という
// 再現しにくい不具合になる。
func (p *Peer) AddCandidate(ci Candidate) error {
	p.mu.Lock()
	if !p.remoteSet {
		p.bufferLocked(ci)
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	if err := p.pc.AddICECandidate(ci.toPion()); err != nil {
		p.mu.Lock()
		p.rejected++
		p.mu.Unlock()
	}
	return nil
}

// bufferLocked は candidate を1つ溜める。p.mu を保持したまま呼ぶこと。
//
// 溢れたときに新しいほうを捨ててはならない。役に立たない candidate を上限ぶん
// 先に送りつけるだけで、あとから来る本物を1つ残らず締め出せてしまうからである。
// それは「記憶を食う迷惑」を「確実な対戦の妨害」に取り替える悪い交換である。
// だから古いほうから捨てる。本物は遅れて届くほうなので、必ず場所が空いている。
//
// 同じ candidate は畳む。同じものを繰り返す手は仕掛けるのが最も簡単なので、
// ここを潰しておく価値が大きい。相異なるものを大量に用意するのは
// はるかに難しく、それには古いほうから捨てる規則で対処する。
func (p *Peer) bufferLocked(ci Candidate) {
	if p.pendingSeen == nil {
		p.pendingSeen = make(map[candidateKey]struct{})
	}
	k := ci.key()
	if _, dup := p.pendingSeen[k]; dup {
		p.rejected++
		return
	}
	for len(p.pending) >= maxPendingCandidates {
		delete(p.pendingSeen, p.pending[0].key())
		p.pending = p.pending[1:]
		p.rejected++
	}
	p.pending = append(p.pending, ci)
	p.pendingSeen[k] = struct{}{}
}

// SetRemote は相手の SDP を設定し、溜めていた candidate をまとめて流す。
func (p *Peer) SetRemote(sd Description) error {
	if err := p.pc.SetRemoteDescription(sd.toPion()); err != nil {
		return fmt.Errorf("rtc: 相手の SDP を設定できない: %w", err)
	}
	p.mu.Lock()
	p.remoteSet = true
	buffered := p.pending
	p.pending = nil
	p.pendingSeen = nil
	p.mu.Unlock()

	// 解釈できないものは飛ばして、残りを全部流す。
	// candidate は公開のシグナリング経由で、こちらが制御できない相手から届く。
	// 1つでも失敗したらやめる実装だと、壊れた文字列を1つ混ぜるだけで対戦を潰せる。
	// ICE は「使える candidate が1つでもあれば繋がる」設計なので、捨てるのは
	// その1つだけにとどめ、SetRemote 自体は失敗させない。
	rejected := 0
	for _, ci := range buffered {
		if err := p.pc.AddICECandidate(ci.toPion()); err != nil {
			rejected++
		}
	}
	if rejected > 0 {
		p.mu.Lock()
		p.rejected += rejected
		p.mu.Unlock()
	}
	return nil
}

// Transport は DataChannel が開くまで待ち、開いたら netplay.Transport を返す。
// ctx はここで待つ時間の上限であって、返した Transport の寿命ではない。
func (p *Peer) Transport(ctx context.Context) (netplay.Transport, error) {
	// 開いていれば、その後に切れていても先に渡す。切れる直前に届いた分が
	// Transport の中に溜まっていることがあり、それは受け取らせるべきである。
	select {
	case <-p.ready:
		return p.openedTransport()
	default:
	}
	select {
	case <-p.ready:
		return p.openedTransport()
	case <-p.gone:
		return nil, netplay.ErrPeerGone
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// openedTransport は ready が閉じたあとの Transport を返す。
func (p *Peer) openedTransport() (netplay.Transport, error) {
	t := p.transport()
	if t == nil {
		// ready が閉じている以上ここには来ないが、来たなら経路は無い。
		return nil, netplay.ErrPeerGone
	}
	return t, nil
}

func (p *Peer) transport() *dcTransport {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.tr == nil {
		return nil
	}
	return p.tr
}

// markGone は接続が失われたことを記録し、既に配った Transport にも伝える。
func (p *Peer) markGone() {
	p.goneOnce.Do(func() { close(p.gone) })
	if t := p.transport(); t != nil {
		t.markGone()
	}
}

// Close は接続を閉じる。二重に呼んでもよい。
func (p *Peer) Close() error {
	var err error
	p.closeOnce.Do(func() {
		p.markGone()
		// 相手から来た外部入力を抱えたまま残さない。
		// 長寿命のタブで対戦を繰り返すので、終わった Peer は手ぶらにしておく。
		p.mu.Lock()
		p.pending = nil
		p.pendingSeen = nil
		p.localPending = nil
		p.onLocal = nil
		p.mu.Unlock()
		err = p.pc.Close()
	})
	return err
}

// dcTransport は DataChannel を netplay.Transport に見せる。
type dcTransport struct {
	dc     *webrtc.DataChannel
	in     chan []byte
	closed chan struct{}
	once   sync.Once
}

func newDCTransport(dc *webrtc.DataChannel) *dcTransport {
	t := &dcTransport{dc: dc, in: make(chan []byte, 64), closed: make(chan struct{})}
	dc.OnMessage(func(m webrtc.DataChannelMessage) {
		select {
		case t.in <- append([]byte(nil), m.Data...):
		case <-t.closed:
		}
	})
	// OnClose はここで仕掛けない。ハンドラは1つしか持てないので、
	// Peer 側の始末と一緒に watchDataChannel が持つ。
	return t
}

// markGone は経路が失われたことを記録する。二重に呼んでもよい。
func (t *dcTransport) markGone() {
	t.once.Do(func() { close(t.closed) })
}

func (t *dcTransport) Send(b []byte) error {
	select {
	case <-t.closed:
		return netplay.ErrPeerGone
	default:
	}
	if err := t.dc.Send(b); err != nil {
		// 送れないのは相手が居ないときなので、上位層には ErrPeerGone として見せる。
		return fmt.Errorf("%w: %v", netplay.ErrPeerGone, err)
	}
	return nil
}

// Receive は netplay.Transport の契約に従う。
//
// 既に届いている分を先に非ブロッキングで覗くのが要点である。ここで select に任せると、
// 届いていても擬似ランダムに ctx.Err() を返す。呼び出し側（netplay.Session.drain）は
// 毎フレーム「期限0の context」を「届いている分だけ寄越せ」の意味で渡すため、
// context を先に見る実装だと、相手から届いていても永遠に受け取れない。
//
// 何も届かないまま締切が来たときは ctx.Err() をそのまま返す。これは
// context.DeadlineExceeded か context.Canceled を満たし、上位層に「届いていないだけ」と
// 伝わる。ここで別のエラーを返すと、何も起きていない毎フレームの Poll が対戦を終わらせる。
func (t *dcTransport) Receive(ctx context.Context) ([]byte, error) {
	select {
	case b := <-t.in:
		return b, nil
	default:
	}
	select {
	case b := <-t.in:
		return b, nil
	case <-t.closed:
		return nil, netplay.ErrPeerGone
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *dcTransport) Close() error {
	t.markGone()
	return t.dc.Close()
}
