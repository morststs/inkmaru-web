package netplay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/morststs/inkmaru-web/internal/rules"
	"github.com/morststs/inkmaru-web/internal/ui"
)

// 偽のシグナリングで、状態遷移だけを検証する。
//
// 送った要求は data を JSON にしてから覚える。Connector が何を線に出したかを
// 中身まで確かめないと、「offer を送ったつもりで空を送っていた」を見逃す。
type sentReq struct {
	action string
	room   string
	data   []byte
}

type fakeSignal struct {
	mu     sync.Mutex
	events []SigEvent
	sent   []sentReq
	sendEr error
	closed int
	nextN  int // Next を呼ばれた回数。「閉じたあとは触らない」の検査に使う
}

func (f *fakeSignal) Send(action string, room string, data any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendEr != nil {
		return f.sendEr
	}
	var raw []byte
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return err
		}
		raw = b
	}
	f.sent = append(f.sent, sentReq{action: action, room: room, data: raw})
	return nil
}

func (f *fakeSignal) Next() (SigEvent, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextN++
	if len(f.events) == 0 {
		return SigEvent{}, false
	}
	e := f.events[0]
	f.events = f.events[1:]
	return e, true
}

func (f *fakeSignal) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return nil
}

// nextCalls は Next を呼ばれた回数を返す。
func (f *fakeSignal) nextCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nextN
}

// sentCount は送った要求の数を返す。
func (f *fakeSignal) sentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

// queued はまだ取り出されていない通知の数を返す。
// 1フレームで何件消費したかを外から数えるために使う。
func (f *fakeSignal) queued() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

// closes は Close を呼ばれた回数を返す。
func (f *fakeSignal) closes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// actions は送った要求の action だけを並べる。
func (f *fakeSignal) actions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.sent))
	for i, s := range f.sent {
		out[i] = s.action
	}
	return out
}

// signals は送った signal の中身を種別ごとに取り出す。
func (f *fakeSignal) signals(kind string) [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]byte
	for _, s := range f.sent {
		if s.action != "signal" {
			continue
		}
		var env struct {
			Kind string          `json:"kind"`
			Body json.RawMessage `json:"body"`
		}
		if err := json.Unmarshal(s.data, &env); err != nil {
			continue
		}
		if env.Kind == kind {
			out = append(out, []byte(env.Body))
		}
	}
	return out
}

// 偽の RTC。SDP と candidate は不透明なバイト列として扱う。
type fakePeer struct {
	mu       sync.Mutex
	host     bool
	local    []byte
	localErr error
	descN    int
	remote   [][]byte
	remoteEr error
	cands    [][]byte
	onLocal  func([]byte)
	tr       Transport
	trErr    error
	trN      int // Transport を呼ばれた回数
	closed   int
}

func (p *fakePeer) LocalDescription() ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.descN++
	if p.localErr != nil {
		return nil, p.localErr
	}
	return p.local, nil
}

func (p *fakePeer) SetRemote(sdp []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.remoteEr != nil {
		return p.remoteEr
	}
	p.remote = append(p.remote, append([]byte(nil), sdp...))
	return nil
}

func (p *fakePeer) AddCandidate(c []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cands = append(p.cands, append([]byte(nil), c...))
	return nil
}

func (p *fakePeer) OnLocalCandidate(f func([]byte)) {
	p.mu.Lock()
	p.onLocal = f
	p.mu.Unlock()
}

// emit は自分の candidate が出たことにする。
func (p *fakePeer) emit(c []byte) {
	p.mu.Lock()
	f := p.onLocal
	p.mu.Unlock()
	if f != nil {
		f(c)
	}
}

// open は DataChannel が開いたことにする。
func (p *fakePeer) open(t Transport) {
	p.mu.Lock()
	p.tr = t
	p.mu.Unlock()
}

func (p *fakePeer) Transport(ctx context.Context) (Transport, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.trN++
	if p.tr != nil {
		return p.tr, nil
	}
	if p.trErr != nil {
		return nil, p.trErr
	}
	// まだ開いていない。契約どおり context のエラーで「届いていない」を伝える。
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, context.DeadlineExceeded
}

func (p *fakePeer) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed++
	return nil
}

// descCalls は LocalDescription を呼ばれた回数を返す。
func (p *fakePeer) descCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.descN
}

// transportCalls は Transport を呼ばれた回数を返す。
func (p *fakePeer) transportCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.trN
}

// closes は Close を呼ばれた回数を返す。
func (p *fakePeer) closes() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

func (p *fakePeer) candidates() [][]byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]byte(nil), p.cands...)
}

func (p *fakePeer) remotes() [][]byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]byte(nil), p.remote...)
}

// 偽の時計。制限時間の検証に使う。
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// newTestConnector は偽物3つを繋いだ Connector を作る。
func newTestConnector(f *fakeSignal, p *fakePeer, clk *fakeClock) *Connector {
	return NewConnectorWith(f, func(host bool) (RTCPeer, error) {
		p.mu.Lock()
		p.host = host
		p.mu.Unlock()
		return p, nil
	}, clk.now)
}

// sigSignal のイベントを組み立てる。
func signalEvent(t *testing.T, kind string, body []byte) SigEvent {
	t.Helper()
	b, err := json.Marshal(struct {
		Kind string          `json:"kind"`
		Body json.RawMessage `json:"body"`
	}{Kind: kind, Body: json.RawMessage(body)})
	if err != nil {
		t.Fatalf("イベントを組み立てられない: %v", err)
	}
	return SigEvent{Kind: SigSignal, Data: b}
}

func TestHostFlowReachesWaitingPeer(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{{Kind: SigCreated, Room: "P2Z5BS"}}}
	c := newTestConnector(f, &fakePeer{}, newClock())
	c.StartHost()
	if got := c.Update(); got != StateCreating {
		t.Fatalf("送信直後の状態 = %v, 期待 StateCreating", got)
	}
	if got := c.Update(); got != StateWaitingPeer {
		t.Fatalf("created 受信後の状態 = %v, 期待 StateWaitingPeer", got)
	}
	if c.Room() != "P2Z5BS" {
		t.Errorf("部屋番号 = %q", c.Room())
	}
	if got := f.actions(); len(got) != 1 || got[0] != "create" {
		t.Errorf("送った要求 = %v, 期待 [create]", got)
	}
}

func TestGuestJoinFailureIsReported(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{{Kind: SigError, Code: "room-not-found"}}}
	c := newTestConnector(f, &fakePeer{}, newClock())
	c.StartGuest("NOPE12")
	c.Update()
	if got := c.Update(); got != StateFailed {
		t.Fatalf("状態 = %v, 期待 StateFailed", got)
	}
	if c.FailReason() == "" {
		t.Fatal("失敗の理由が空")
	}
	// 理由には元のコードを残す。画面には日本語を出すが、
	// どのコードで落ちたかが分からないと問い合わせに答えられない。
	if !strings.Contains(c.FailReason(), "room-not-found") {
		t.Errorf("失敗の理由 = %q, room-not-found を含んでほしい", c.FailReason())
	}
	if got := f.actions(); len(got) != 1 || got[0] != "join" {
		t.Errorf("送った要求 = %v, 期待 [join]", got)
	}
	if f.sent[0].room != "NOPE12" {
		t.Errorf("join の部屋 = %q, 期待 NOPE12", f.sent[0].room)
	}
}

func TestPeerLeftBeforeConnectFails(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{
		{Kind: SigCreated, Room: "ABC123"},
		{Kind: SigPeerLeft},
	}}
	c := newTestConnector(f, &fakePeer{}, newClock())
	c.StartHost()
	for i := 0; i < 5 && c.Update() != StateFailed; i++ {
	}
	if c.Update() != StateFailed {
		t.Error("接続前の peer-left で失敗にならなかった")
	}
	if c.FailReason() == "" {
		t.Error("失敗の理由が空")
	}
}

// ホストは peer-joined で offer を作って送り、接続中に移る。
func TestHostSendsOfferOnPeerJoined(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{
		{Kind: SigCreated, Room: "ABC123"},
		{Kind: SigPeerJoined},
	}}
	p := &fakePeer{local: []byte(`{"type":"offer","sdp":"HOST"}`)}
	c := newTestConnector(f, p, newClock())
	c.StartHost()
	c.Update() // create 送信
	if got := c.Update(); got != StateConnecting {
		t.Fatalf("peer-joined 後の状態 = %v, 期待 StateConnecting", got)
	}
	sdps := f.signals("sdp")
	if len(sdps) != 1 {
		t.Fatalf("送った sdp = %d 件, 期待 1 件", len(sdps))
	}
	if string(sdps[0]) != `{"type":"offer","sdp":"HOST"}` {
		t.Errorf("送った offer = %s", sdps[0])
	}
	if !p.host {
		t.Error("ホストなのに offerer として Peer を作っていない")
	}
}

// signal が peer-joined より先に届いても壊れない（到着順は保証されない）。
func TestHostHandlesSignalBeforePeerJoined(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{
		{Kind: SigCreated, Room: "ABC123"},
		signalEvent(t, "ice", []byte(`{"candidate":"EARLY"}`)),
		{Kind: SigPeerJoined},
	}}
	p := &fakePeer{local: []byte(`{"type":"offer","sdp":"HOST"}`)}
	c := newTestConnector(f, p, newClock())
	c.StartHost()
	c.Update()
	if got := c.Update(); got != StateConnecting {
		t.Fatalf("状態 = %v, 期待 StateConnecting", got)
	}
	cands := p.candidates()
	if len(cands) != 1 || string(cands[0]) != `{"candidate":"EARLY"}` {
		t.Fatalf("先着の candidate が Peer に渡っていない: %q", cands)
	}
	// peer-joined が後から来ても offer は1通だけ。
	if n := len(f.signals("sdp")); n != 1 {
		t.Errorf("送った offer = %d 件, 期待 1 件", n)
	}
}

// ゲストは offer を受け取ってから answer を返す。offer より前には何も送らない。
func TestGuestAnswersOffer(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{{Kind: SigJoined, Room: "ABC123"}}}
	p := &fakePeer{local: []byte(`{"type":"answer","sdp":"GUEST"}`)}
	clk := newClock()
	c := newTestConnector(f, p, clk)
	c.StartGuest("ABC123")
	c.Update() // join 送信
	if got := c.Update(); got != StateConnecting {
		t.Fatalf("joined 後の状態 = %v, 期待 StateConnecting", got)
	}
	if n := len(f.signals("sdp")); n != 0 {
		t.Fatalf("offer より前に sdp を %d 件送った", n)
	}
	f.mu.Lock()
	f.events = append(f.events, signalEvent(t, "sdp", []byte(`{"type":"offer","sdp":"HOST"}`)))
	f.mu.Unlock()
	c.Update()
	rem := p.remotes()
	if len(rem) != 1 || string(rem[0]) != `{"type":"offer","sdp":"HOST"}` {
		t.Fatalf("相手の SDP を設定していない: %q", rem)
	}
	ans := f.signals("sdp")
	if len(ans) != 1 || string(ans[0]) != `{"type":"answer","sdp":"GUEST"}` {
		t.Fatalf("answer を送っていない: %q", ans)
	}
}

// 2通目の SDP は捨てる。相手が offer を撃ち直しても状態を壊さない。
func TestDuplicateSDPIsIgnored(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{
		{Kind: SigJoined, Room: "ABC123"},
		signalEvent(t, "sdp", []byte(`{"type":"offer","sdp":"HOST"}`)),
		signalEvent(t, "sdp", []byte(`{"type":"offer","sdp":"AGAIN"}`)),
	}}
	p := &fakePeer{local: []byte(`{"type":"answer","sdp":"GUEST"}`)}
	c := newTestConnector(f, p, newClock())
	c.StartGuest("ABC123")
	c.Update()
	c.Update()
	if n := len(p.remotes()); n != 1 {
		t.Errorf("SetRemote = %d 回, 期待 1 回", n)
	}
	if n := len(f.signals("sdp")); n != 1 {
		t.Errorf("送った answer = %d 件, 期待 1 件", n)
	}
}

// 壊れた signal は捨てる。1通で対戦を潰せてはならない。
func TestMalformedSignalIsDropped(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{
		{Kind: SigJoined, Room: "ABC123"},
		{Kind: SigSignal, Data: []byte(`{"kind":`)},
		{Kind: SigSignal, Data: []byte(`{"kind":"unknown","body":{}}`)},
	}}
	p := &fakePeer{local: []byte(`{"type":"answer","sdp":"GUEST"}`)}
	c := newTestConnector(f, p, newClock())
	c.StartGuest("ABC123")
	c.Update()
	if got := c.Update(); got != StateConnecting {
		t.Fatalf("壊れた signal で状態が %v になった, 期待 StateConnecting", got)
	}
}

// **中身が悪いだけの SDP 1通で対戦が死んではならない。**
//
// シグナリングはこちらが制御できない相手から届く場所である。candidate は元から
// 1つ捨てるだけで済ませていたのに、SDP の枝だけが「1通で対戦を潰せるように
// してはならない」という applySignal の方針から外れていた。ErrBadSignal を包んだ
// 失敗はその1通を捨て、あとから届いた正しい SDP は受け取れること。
func TestBadSDPIsDroppedAndLaterOfferStillWorks(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{
		{Kind: SigJoined, Room: "ABC123"},
		signalEvent(t, "sdp", []byte(`{"type":"どっちでもない","sdp":"v=0"}`)),
	}}
	p := &fakePeer{local: []byte(`{"type":"answer","sdp":"GUEST"}`)}
	p.remoteEr = fmt.Errorf("%w: 知らない SDP の種別", ErrBadSignal)
	c := newTestConnector(f, p, newClock())
	c.StartGuest("ABC123")
	c.Update() // join 送信
	if got := c.Update(); got != StateConnecting {
		t.Fatalf("壊れた SDP で状態が %v になった, 期待 StateConnecting（%s）", got, c.FailReason())
	}
	if n := len(f.signals("sdp")); n != 0 {
		t.Errorf("壊れた SDP に answer を返した: %d 件", n)
	}

	// 正しい offer があとから届けば、こんどは受け取って answer を返すこと。
	// 捨てた1通で remoteSDP を立ててしまうと、ここが黙って死ぬ。
	p.mu.Lock()
	p.remoteEr = nil
	p.mu.Unlock()
	f.mu.Lock()
	f.events = append(f.events, signalEvent(t, "sdp", []byte(`{"type":"offer","sdp":"HOST"}`)))
	f.mu.Unlock()
	if got := c.Update(); got != StateConnecting {
		t.Fatalf("正しい offer で状態が %v になった: %s", got, c.FailReason())
	}
	if rem := p.remotes(); len(rem) != 1 || string(rem[0]) != `{"type":"offer","sdp":"HOST"}` {
		t.Fatalf("あとから届いた正しい SDP を設定していない: %q", rem)
	}
	if ans := f.signals("sdp"); len(ans) != 1 {
		t.Errorf("answer = %d 件, 期待 1 件", len(ans))
	}
}

// ただしピア自体の故障は接続の死である。ErrBadSignal を包まない失敗まで
// 捨てると、本当に壊れているのに30秒の締切まで原因が分からないまま待つ。
func TestPeerFailureOnSetRemoteIsFatal(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{
		{Kind: SigJoined, Room: "ABC123"},
		signalEvent(t, "sdp", []byte(`{"type":"offer","sdp":"HOST"}`)),
	}}
	p := &fakePeer{local: []byte(`{"type":"answer","sdp":"GUEST"}`)}
	p.remoteEr = errors.New("ICE agent が死んでいる")
	c := newTestConnector(f, p, newClock())
	c.StartGuest("ABC123")
	c.Update()
	if got := c.Update(); got != StateFailed {
		t.Fatalf("ピアの故障で状態が %v になった, 期待 StateFailed", got)
	}
	if !strings.Contains(c.FailReason(), "ICE agent が死んでいる") {
		t.Errorf("理由に元のエラーが無い: %q", c.FailReason())
	}
}

// **切断は切断として読めること。**
//
// disconnected はサーバが送ってくるコードではなく、配線層が転送の死に対して
// 合成するものである（Signaller の注釈）。既定の「サーバがエラーを返しました」の
// ままだと、線が切れただけなのに「サーバの都合で断られた」と読め、
// プレイヤーも問い合わせを受けた側も原因の見当を正反対に付ける。
func TestDisconnectedReadsAsALostConnection(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{{Kind: SigError, Code: "disconnected"}}}
	c := newTestConnector(f, &fakePeer{}, newClock())
	c.StartHost()
	c.Update() // create 送信
	if got := c.Update(); got != StateFailed {
		t.Fatalf("状態 = %v, 期待 StateFailed", got)
	}
	got := c.FailReason()
	if !strings.Contains(got, "接続が切れました") {
		t.Errorf("理由 = %q, 切断として読めない", got)
	}
	if strings.Contains(got, "サーバがエラーを返しました") {
		t.Errorf("理由 = %q, 切断が「サーバの都合で断られた」と読める", got)
	}
	// 問い合わせに答えるにはコードが要る。ほかの理由と同じく残すこと。
	if !strings.Contains(got, "disconnected") {
		t.Errorf("理由 = %q, 元のコードが残っていない", got)
	}
}

// 自分の candidate は signal として中継してもらう。
func TestLocalCandidatesAreRelayed(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{
		{Kind: SigCreated, Room: "ABC123"},
		{Kind: SigPeerJoined},
	}}
	p := &fakePeer{local: []byte(`{"type":"offer","sdp":"HOST"}`)}
	c := newTestConnector(f, p, newClock())
	c.StartHost()
	c.Update()
	c.Update()
	p.emit([]byte(`{"candidate":"MINE"}`))
	c.Update()
	ice := f.signals("ice")
	if len(ice) != 1 || string(ice[0]) != `{"candidate":"MINE"}` {
		t.Fatalf("自分の candidate を中継していない: %q", ice)
	}
}

// 開通したら Ready になり、相手が使えるようになる。
// **その瞬間に NetOpponent.Start() を呼ぶこと。** 呼ばないと最初のターンの
// 30秒が「初めて Deadline を見た時刻」から数え始まり、黙って延びる。
func TestReadyStartsOpponentClock(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{
		{Kind: SigJoined, Room: "ABC123"},
		signalEvent(t, "sdp", []byte(`{"type":"offer","sdp":"HOST"}`)),
	}}
	p := &fakePeer{local: []byte(`{"type":"answer","sdp":"GUEST"}`)}
	clk := newClock()
	c := newTestConnector(f, p, clk)
	c.StartGuest("ABC123")
	c.Update()
	c.Update()
	if c.Opponent() != nil {
		t.Fatal("開通前に Opponent が返った")
	}
	a, _ := NewPipe()
	p.open(a)
	readyAt := clk.now()
	if got := c.Update(); got != StateReady {
		t.Fatalf("開通後の状態 = %v, 期待 StateReady", got)
	}
	o := c.Opponent()
	if o == nil {
		t.Fatal("Ready なのに Opponent が nil")
	}
	// Ready から5秒後に初めて期限を見る。Start が呼ばれていれば
	// 起点は Ready の瞬間なので、期限は5秒ぶん動かない。
	clk.advance(5 * time.Second)
	d, ok := o.Deadline(0)
	if !ok {
		t.Fatal("ターン0の期限が立っていない")
	}
	if want := readyAt.Add(TurnLimit); !d.Equal(want) {
		t.Errorf("ターン0の期限 = %v, 期待 %v（Start が開通時に呼ばれていない）", d, want)
	}
	if got := c.Update(); got != StateReady {
		t.Errorf("Ready のあとの状態 = %v", got)
	}
}

// 席は役割で決まる。両者が同じ席だと第1ターンで board-mismatch になる。
func TestSeatsDifferByRole(t *testing.T) {
	host := newTestConnector(&fakeSignal{}, &fakePeer{}, newClock())
	host.StartHost()
	guest := newTestConnector(&fakeSignal{}, &fakePeer{}, newClock())
	guest.StartGuest("ABC123")
	if got := host.Seat(); got != rules.P1 {
		t.Errorf("ホストの席 = %v, 期待 P1", got)
	}
	if got := guest.Seat(); got != rules.P2 {
		t.Errorf("ゲストの席 = %v, 期待 P2", got)
	}
}

// 接続は30秒で打ち切る（仕様書 §7.4）。
func TestConnectingTimesOutAfter30s(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{{Kind: SigJoined, Room: "ABC123"}}}
	clk := newClock()
	c := newTestConnector(f, &fakePeer{}, clk)
	c.StartGuest("ABC123")
	c.Update()
	c.Update()
	clk.advance(ConnectTimeout - time.Second)
	if got := c.Update(); got != StateConnecting {
		t.Fatalf("29秒での状態 = %v, 期待 StateConnecting", got)
	}
	clk.advance(2 * time.Second)
	if got := c.Update(); got != StateFailed {
		t.Fatalf("31秒での状態 = %v, 期待 StateFailed", got)
	}
	if c.FailReason() == "" {
		t.Error("失敗の理由が空")
	}
}

// 相手待ちは30秒では切らない。部屋番号を口頭で伝えて入力してもらうには
// 30秒では足りず、切ると部屋を作る機能そのものが使えない。
// 打ち切りは部屋の寿命（サーバ側の TTL 10分）に合わせる。
func TestWaitingPeerIsNotCutOffAt30s(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{{Kind: SigCreated, Room: "ABC123"}}}
	clk := newClock()
	c := newTestConnector(f, &fakePeer{}, clk)
	c.StartHost()
	c.Update()
	c.Update()
	clk.advance(2 * ConnectTimeout)
	if got := c.Update(); got != StateWaitingPeer {
		t.Fatalf("60秒での状態 = %v, 期待 StateWaitingPeer", got)
	}
	clk.advance(WaitPeerTimeout)
	if got := c.Update(); got != StateFailed {
		t.Fatalf("部屋の寿命を超えた状態 = %v, 期待 StateFailed", got)
	}
}

// Ready は終着点。対戦中に signal 経由の peer-left が来ても、
// 状態機械はもう対戦に口を出さない（切断の判定は DataChannel が持つ）。
func TestReadyIsTerminal(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{
		{Kind: SigJoined, Room: "ABC123"},
		signalEvent(t, "sdp", []byte(`{"type":"offer","sdp":"HOST"}`)),
	}}
	p := &fakePeer{local: []byte(`{"type":"answer","sdp":"GUEST"}`)}
	clk := newClock()
	c := newTestConnector(f, p, clk)
	c.StartGuest("ABC123")
	c.Update()
	c.Update()
	a, _ := NewPipe()
	p.open(a)
	if got := c.Update(); got != StateReady {
		t.Fatalf("状態 = %v, 期待 StateReady", got)
	}
	f.mu.Lock()
	f.events = append(f.events, SigEvent{Kind: SigPeerLeft})
	f.mu.Unlock()
	clk.advance(time.Hour)
	if got := c.Update(); got != StateReady {
		t.Errorf("Ready のあとの peer-left で状態が %v になった", got)
	}
}

// Ready のあとも candidate の中継は続ける。ICE は開通後も経路を足しに来る。
func TestReadyStillAppliesCandidates(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{
		{Kind: SigJoined, Room: "ABC123"},
		signalEvent(t, "sdp", []byte(`{"type":"offer","sdp":"HOST"}`)),
	}}
	p := &fakePeer{local: []byte(`{"type":"answer","sdp":"GUEST"}`)}
	c := newTestConnector(f, p, newClock())
	c.StartGuest("ABC123")
	c.Update()
	c.Update()
	a, _ := NewPipe()
	p.open(a)
	c.Update()
	f.mu.Lock()
	f.events = append(f.events, signalEvent(t, "ice", []byte(`{"candidate":"LATE"}`)))
	f.mu.Unlock()
	c.Update()
	cands := p.candidates()
	if len(cands) != 1 || string(cands[0]) != `{"candidate":"LATE"}` {
		t.Errorf("開通後の candidate が Peer に渡っていない: %q", cands)
	}
}

// Close は両方を閉じる。二度呼んでも一度しか閉じない。
func TestCloseClosesBoth(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{
		{Kind: SigCreated, Room: "ABC123"},
		{Kind: SigPeerJoined},
	}}
	p := &fakePeer{local: []byte(`{"type":"offer","sdp":"HOST"}`)}
	c := newTestConnector(f, p, newClock())
	c.StartHost()
	c.Update()
	c.Update()
	c.Close()
	c.Close()
	if f.closed != 1 {
		t.Errorf("シグナリングを閉じた回数 = %d, 期待 1", f.closed)
	}
	if p.closed != 1 {
		t.Errorf("Peer を閉じた回数 = %d, 期待 1", p.closed)
	}
}

// 送信そのものが失敗したら、そこで諦める。
func TestSendFailureFails(t *testing.T) {
	f := &fakeSignal{sendEr: errors.New("接続が切れている")}
	c := newTestConnector(f, &fakePeer{}, newClock())
	c.StartHost()
	if got := c.Update(); got != StateFailed {
		t.Fatalf("状態 = %v, 期待 StateFailed", got)
	}
	if c.FailReason() == "" {
		t.Error("失敗の理由が空")
	}
}

// Transport が context 以外のエラーを返したら失敗（相手が消えた等）。
func TestTransportErrorFails(t *testing.T) {
	p := &fakePeer{local: []byte(`{"type":"answer","sdp":"GUEST"}`), trErr: ErrPeerGone}
	f := &fakeSignal{events: []SigEvent{
		{Kind: SigJoined, Room: "ABC123"},
		signalEvent(t, "sdp", []byte(`{"type":"offer","sdp":"HOST"}`)),
	}}
	c := newTestConnector(f, p, newClock())
	c.StartGuest("ABC123")
	c.Update()
	if got := c.Update(); got != StateFailed {
		t.Fatalf("状態 = %v, 期待 StateFailed", got)
	}
	if c.FailReason() == "" {
		t.Error("失敗の理由が空")
	}
}

// ゲストは自分から SDP を送り出してはならない。offer を作るのはホストだけである。
//
// **この検査は「SDP より先に ICE の signal が届く」順序でしか歯が立たない。**
// joined も peer-joined も peerPresent を通らない／通っても sentSDP が
// 二重送信を畳むため、役割の取り違え（ホスト限定の番兵を落とすこと）が
// そこには現れない。この到着順は第2段階a §11 の実測どおりで、実際に起きる。
// 本物の Peer では、遠隔記述の前に CreateAnswer を呼ぶと必ず失敗するので、
// 取り違えるとオンライン対戦が1件も成立しなくなる。
func TestGuestNeverSendsSDPBeforeOffer(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{
		{Kind: SigJoined, Room: "ABC123"},
		signalEvent(t, "ice", []byte(`{"candidate":"EARLY"}`)),
	}}
	p := &fakePeer{local: []byte(`{"type":"answer","sdp":"GUEST"}`)}
	c := newTestConnector(f, p, newClock())
	c.StartGuest("ABC123")
	c.Update() // join 送信
	if got := c.Update(); got != StateConnecting {
		t.Fatalf("状態 = %v, 期待 StateConnecting", got)
	}
	// 相手が居ることは分かった（Peer は作った）。それでも SDP は送らない。
	if sdps := f.signals("sdp"); len(sdps) != 0 {
		t.Fatalf("ゲストが offer より先に sdp を %d 件送った: %q", len(sdps), sdps)
	}
	if n := p.descCalls(); n != 0 {
		t.Errorf("LocalDescription = %d 回, 期待 0 回（遠隔記述より前に呼んではならない）", n)
	}
	if n := len(p.remotes()); n != 0 {
		t.Errorf("SetRemote = %d 回, 期待 0 回", n)
	}
	// 先着の candidate は捨てずに Peer へ渡す。
	cands := p.candidates()
	if len(cands) != 1 || string(cands[0]) != `{"candidate":"EARLY"}` {
		t.Errorf("先着の candidate が Peer に渡っていない: %q", cands)
	}
}

// Close のあとの Update は何もしない。
//
// **開通させた Peer で閉じること。** 開通していない Peer では、番兵を外しても
// どのみち Ready へ進めないので、この検査は原理的に落ちない（旧版がそうだった）。
func TestUpdateAfterCloseDoesNothing(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{
		{Kind: SigJoined, Room: "ABC123"},
		signalEvent(t, "sdp", []byte(`{"type":"offer","sdp":"HOST"}`)),
	}}
	p := &fakePeer{local: []byte(`{"type":"answer","sdp":"GUEST"}`)}
	c := newTestConnector(f, p, newClock())
	c.StartGuest("ABC123")
	c.Update()
	if got := c.Update(); got != StateConnecting {
		t.Fatalf("状態 = %v, 期待 StateConnecting", got)
	}
	a, _ := NewPipe()
	p.open(a) // このまま Update を呼べば Ready へ進む状態
	c.Close()
	next, sent := f.nextCalls(), f.sentCount()
	tr, desc := p.transportCalls(), p.descCalls()
	if got := c.Update(); got == StateReady {
		t.Fatalf("閉じたあとの Update が Ready へ進んだ")
	}
	if c.Opponent() != nil {
		t.Error("閉じたあとに Opponent が作られた")
	}
	if f.nextCalls() != next || f.sentCount() != sent {
		t.Errorf("閉じたあとにシグナリングへ触った: Next %d→%d, Send %d→%d",
			next, f.nextCalls(), sent, f.sentCount())
	}
	if p.transportCalls() != tr || p.descCalls() != desc {
		t.Errorf("閉じたあとに Peer へ触った: Transport %d→%d, LocalDescription %d→%d",
			tr, p.transportCalls(), desc, p.descCalls())
	}
}

// Ready に達したあとの Close は対戦の経路ごと切る。
//
// **Ready まで進めてから閉じること。** Ready より前は c.opp が nil なので、
// 相手を閉じる行を丸ごと削っても落ちない（TestCloseClosesBoth がそうである）。
// 閉じたことは外から観測する: 経路が切れていれば Submit は
// ui.ErrPeerGone を包んだエラーになる。
func TestCloseAfterReadyClosesOpponent(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{
		{Kind: SigJoined, Room: "ABC123"},
		signalEvent(t, "sdp", []byte(`{"type":"offer","sdp":"HOST"}`)),
	}}
	p := &fakePeer{local: []byte(`{"type":"answer","sdp":"GUEST"}`)}
	c := newTestConnector(f, p, newClock())
	c.StartGuest("ABC123")
	c.Update()
	c.Update()
	a, _ := NewPipe()
	p.open(a)
	if got := c.Update(); got != StateReady {
		t.Fatalf("状態 = %v, 期待 StateReady", got)
	}
	o := c.Opponent()
	if o == nil {
		t.Fatal("Ready なのに Opponent が nil")
	}
	// 閉じる前は送れる。ここが送れないなら、あとの検査は何も意味しない。
	if err := o.Submit(rules.Circle{CX: 0.5, CY: 0.5, R: 0.1}, 0); err != nil {
		t.Fatalf("Close の前の Submit = %v, 期待 nil", err)
	}
	c.Close()
	err := o.Submit(rules.Circle{CX: 0.5, CY: 0.5, R: 0.1}, 1)
	if !errors.Is(err, ui.ErrPeerGone) {
		t.Fatalf("Close 後の Submit = %v, 期待 ui.ErrPeerGone を包んだエラー（相手が閉じられていない）", err)
	}
}

// StartHost の二度押しは効かない。効くと create が2通飛び、締切が伸びる。
func TestSecondStartHostIsIgnored(t *testing.T) {
	f := &fakeSignal{}
	clk := newClock()
	c := newTestConnector(f, &fakePeer{}, clk)
	c.StartHost()
	c.Update() // create 送信。ここから ConnectTimeout を数える
	clk.advance(20 * time.Second)
	c.StartHost() // 二度押し
	c.Update()
	if got := f.actions(); len(got) != 1 || got[0] != "create" {
		t.Fatalf("送った要求 = %v, 期待 [create] 1通だけ", got)
	}
	// 締切は最初の create の時刻から動かない。
	clk.advance(ConnectTimeout - 20*time.Second + time.Second)
	if got := c.Update(); got != StateFailed {
		t.Fatalf("最初の create から31秒後の状態 = %v, 期待 StateFailed（締切が押し直された）", got)
	}
}

// StartGuest の二度押しも効かない。
func TestSecondStartGuestIsIgnored(t *testing.T) {
	f := &fakeSignal{}
	clk := newClock()
	c := newTestConnector(f, &fakePeer{}, clk)
	c.StartGuest("ABC123")
	c.Update() // join 送信
	clk.advance(20 * time.Second)
	c.StartGuest("ZZZ999") // 二度押し
	c.Update()
	if got := f.actions(); len(got) != 1 || got[0] != "join" {
		t.Fatalf("送った要求 = %v, 期待 [join] 1通だけ", got)
	}
	if c.Room() != "ABC123" {
		t.Errorf("部屋番号 = %q, 期待 ABC123", c.Room())
	}
	clk.advance(ConnectTimeout - 20*time.Second + time.Second)
	if got := c.Update(); got != StateFailed {
		t.Fatalf("最初の join から31秒後の状態 = %v, 期待 StateFailed（締切が押し直された）", got)
	}
}

// 失敗の理由は最初の1つを残す。あとから起きた失敗で上書きしない。
//
// 同じ1フレームで2度失敗させる: candidate の中継が送信に失敗した直後、
// 同じ Update の通知の取り出しでサーバのエラーを受け取る。画面に出るのは
// 「なぜ最初に崩れたか」であるべきで、その巻き添えで起きた2つ目ではない。
func TestFailReasonKeepsFirstCause(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{
		{Kind: SigCreated, Room: "ABC123"},
		{Kind: SigPeerJoined},
	}}
	p := &fakePeer{local: []byte(`{"type":"offer","sdp":"HOST"}`)}
	c := newTestConnector(f, p, newClock())
	c.StartHost()
	c.Update()
	if got := c.Update(); got != StateConnecting {
		t.Fatalf("状態 = %v, 期待 StateConnecting", got)
	}
	p.emit([]byte(`{"candidate":"MINE"}`))
	f.mu.Lock()
	f.sendEr = errors.New("接続が切れている")
	f.events = append(f.events, SigEvent{Kind: SigError, Code: "room-full"})
	f.mu.Unlock()
	if got := c.Update(); got != StateFailed {
		t.Fatalf("状態 = %v, 期待 StateFailed", got)
	}
	if !strings.Contains(c.FailReason(), "接続情報を送れませんでした") {
		t.Errorf("失敗の理由 = %q, 最初の理由（接続情報を送れませんでした）を期待", c.FailReason())
	}
	if strings.Contains(c.FailReason(), "room-full") {
		t.Errorf("失敗の理由 = %q, あとから来た理由で上書きされている", c.FailReason())
	}
}

// 1フレームで処理する通知には上限がある。
// 上限が無いと、1フレームの仕事量がサーバの都合次第になる。
func TestUpdateStopsAtMaxEventsPerFrame(t *testing.T) {
	const extra = 5
	// joined は部屋番号を控えるだけで、状態も送信も動かさない。
	// 「何件消費したか」だけを測るのに都合がよい。
	events := make([]SigEvent, 0, maxEventsPerFrame+extra)
	for i := 0; i < maxEventsPerFrame+extra; i++ {
		events = append(events, SigEvent{Kind: SigJoined, Room: "ABC123"})
	}
	f := &fakeSignal{events: events}
	c := newTestConnector(f, &fakePeer{}, newClock())
	c.StartGuest("ABC123")
	c.Update() // join 送信。通知はまだ1件も取り出していない
	if got := f.queued(); got != maxEventsPerFrame+extra {
		t.Fatalf("要求送信の回に通知を %d 件消費した, 期待 0 件", maxEventsPerFrame+extra-got)
	}
	c.Update()
	if got := maxEventsPerFrame + extra - f.queued(); got != maxEventsPerFrame {
		t.Fatalf("1フレームで消費した通知 = %d 件, 期待 %d 件（上限）", got, maxEventsPerFrame)
	}
	c.Update()
	if got := f.queued(); got != 0 {
		t.Errorf("2フレーム目のあとの残り = %d 件, 期待 0 件", got)
	}
}

// 失敗も終着点である。**そこで peer と sig を手放すこと。**
// 本番では pion の ICE agent と goroutine 群・受信 goroutine・開いた WebSocket が
// 居座り、長寿命のブラウザタブでは失敗の試行ごとに漏れる。
func TestFailReleasesPeerAndSignal(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{
		{Kind: SigCreated, Room: "ABC123"},
		{Kind: SigPeerJoined},
	}}
	p := &fakePeer{local: []byte(`{"type":"offer","sdp":"HOST"}`)}
	clk := newClock()
	c := newTestConnector(f, p, clk)
	c.StartHost()
	c.Update()
	if got := c.Update(); got != StateConnecting {
		t.Fatalf("状態 = %v, 期待 StateConnecting", got)
	}
	clk.advance(ConnectTimeout + time.Second)
	if got := c.Update(); got != StateFailed {
		t.Fatalf("状態 = %v, 期待 StateFailed", got)
	}
	if got := p.closes(); got != 1 {
		t.Errorf("失敗後に Peer を閉じた回数 = %d, 期待 1", got)
	}
	if got := f.closes(); got != 1 {
		t.Errorf("失敗後にシグナリングを閉じた回数 = %d, 期待 1", got)
	}
	// 解放するのは資源だけ。画面は理由と状態をこのあと読む。
	if c.State() != StateFailed {
		t.Errorf("解放後の状態 = %v, 期待 StateFailed", c.State())
	}
	if c.FailReason() == "" {
		t.Error("解放後に失敗の理由が読めない")
	}
	// 画面があとから Close を呼んでも、閉じるのは一度きり。
	c.Close()
	c.Close()
	if got := p.closes(); got != 1 {
		t.Errorf("Close 後に Peer を閉じた回数 = %d, 期待 1", got)
	}
	if got := f.closes(); got != 1 {
		t.Errorf("Close 後にシグナリングを閉じた回数 = %d, 期待 1", got)
	}
}

// 2通目の created は部屋番号を動かさない。
// プレイヤーは最初の番号をもう相手へ読み上げている。
func TestSecondCreatedDoesNotChangeRoom(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{
		{Kind: SigCreated, Room: "ABC123"},
		{Kind: SigPeerJoined},
		{Kind: SigCreated, Room: "ZZZ999"},
	}}
	p := &fakePeer{local: []byte(`{"type":"offer","sdp":"HOST"}`)}
	c := newTestConnector(f, p, newClock())
	c.StartHost()
	c.Update()
	if got := c.Update(); got != StateConnecting {
		t.Fatalf("状態 = %v, 期待 StateConnecting", got)
	}
	if c.Room() != "ABC123" {
		t.Errorf("部屋番号 = %q, 期待 ABC123（2通目の created に上書きされた）", c.Room())
	}
}

// P2P が張れずに諦めたときの理由は、見出しの繰り返しではなく説明である。
//
// 画面は見出しに「接続できませんでした」を出し、その下に FailReason を並べる
// （ui.ConnectStatus）。理由が同じ文だと、同じことが2回書いてあるだけの画面になる。
//
// **TURN を入れないと決めた以上、これは一時的な状態ではなく恒久的な答えである**
// （再戦の設計ではなくオンライン対戦の設計 §7.4）。だから理由の欄は
// 「なぜ起きたのか」と「次に何ができるのか」を担う。
func TestP2PTimeoutExplainsInsteadOfRepeating(t *testing.T) {
	f := &fakeSignal{events: []SigEvent{{Kind: SigJoined, Room: "ABC123"}}}
	clk := newClock()
	c := newTestConnector(f, &fakePeer{}, clk)
	c.StartGuest("ABC123")
	c.Update()
	c.Update()
	clk.advance(ConnectTimeout + time.Second)
	if got := c.Update(); got != StateFailed {
		t.Fatalf("状態 = %v, 期待 StateFailed", got)
	}

	reason := c.FailReason()
	if strings.Contains(reason, "接続できませんでした") {
		t.Errorf("理由が見出しを繰り返している: %q", reason)
	}
	if !strings.Contains(reason, "回線") {
		t.Errorf("理由に原因の説明が無い: %q", reason)
	}
	if !strings.Contains(reason, "CPU") {
		t.Errorf("理由に次にできることが無い: %q", reason)
	}
}
