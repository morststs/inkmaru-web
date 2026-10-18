package netwire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/morststs/inkmaru-web/internal/netplay"
	"github.com/morststs/inkmaru-web/internal/rtc"
	"github.com/morststs/inkmaru-web/internal/rules"
	"github.com/morststs/inkmaru-web/internal/sigclient"
	"github.com/morststs/inkmaru-web/internal/ui"
)

// 偽のシグナリング接続。**本物の WebSocket は使わない**
// （配備済みのサーバは課金対象で稼働中であり、この環境は外向き通信も遮断されている）。
//
// Next は sigclient.Client.Next と同じ形にしてある。すなわち届いている分は
// 非ブロッキングに返し、空なら ctx が切れるまで待つ。**この「待つ」を写して
// あることが要点である。** netwire が期限切れでない context を渡す実装に
// 壊れたら、ここで永久に待つので、ブロックの検査が本当に落ちる。
type queued struct {
	ev  sigclient.Event
	err error
}

type sentReq struct {
	action string
	room   string
	data   []byte
	// ctxHasDone は Send に渡された context が Done を持つか。
	// context.Background() なら nil である。期限付きを渡す退行をここで捕まえる。
	ctxHasDone bool
}

type fakeConn struct {
	mu sync.Mutex
	// queue は順に返す通知。尽きたら always を見る。
	queue []queued
	// always は queue が尽きたあと常に返すもの。nil なら ctx を待つ。
	always *queued

	nextCalls int
	// ctxDead は Next に渡された context が呼ばれた時点で既に切れていたか。
	ctxDead []bool

	sends   []sentReq
	sendErr error
	closes  int
}

func (f *fakeConn) push(ev sigclient.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue = append(f.queue, queued{ev: ev})
}

func (f *fakeConn) pushErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue = append(f.queue, queued{err: err})
}

func (f *fakeConn) Next(ctx context.Context) (sigclient.Event, error) {
	f.mu.Lock()
	f.nextCalls++
	f.ctxDead = append(f.ctxDead, ctx.Err() != nil)
	if len(f.queue) > 0 {
		q := f.queue[0]
		f.queue = f.queue[1:]
		f.mu.Unlock()
		return q.ev, q.err
	}
	if f.always != nil {
		q := *f.always
		f.mu.Unlock()
		return q.ev, q.err
	}
	f.mu.Unlock()
	// 本物と同じ。届いていなければ呼び出し側の context が切れるまで待つ。
	<-ctx.Done()
	return sigclient.Event{}, ctx.Err()
}

func (f *fakeConn) record(action, room string, data any, ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return f.sendErr
	}
	var raw []byte
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return err
		}
		raw = b
	}
	f.sends = append(f.sends, sentReq{action: action, room: room, data: raw, ctxHasDone: ctx.Done() != nil})
	return nil
}

func (f *fakeConn) Create(ctx context.Context) error { return f.record("create", "", nil, ctx) }

func (f *fakeConn) Join(ctx context.Context, room string) error {
	return f.record("join", room, nil, ctx)
}

func (f *fakeConn) SendSignal(ctx context.Context, data any) error {
	return f.record("signal", "", data, ctx)
}

func (f *fakeConn) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	return nil
}

func (f *fakeConn) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nextCalls
}

func (f *fakeConn) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closes
}

func (f *fakeConn) sentAt(i int) (sentReq, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.sends) {
		return sentReq{}, false
	}
	return f.sends[i], true
}

// 偽の RTCPeer。pion を使わずに Connector の手順だけを動かす。
type fakePeer struct {
	mu         sync.Mutex
	host       bool
	localCalls int
	remotes    [][]byte
	cands      [][]byte
	closes     int
}

func (p *fakePeer) LocalDescription() ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.localCalls++
	return []byte(`{"type":"offer","sdp":"v=0"}`), nil
}

func (p *fakePeer) SetRemote(sdp []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.remotes = append(p.remotes, append([]byte(nil), sdp...))
	return nil
}

func (p *fakePeer) AddCandidate(c []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cands = append(p.cands, append([]byte(nil), c...))
	return nil
}

func (p *fakePeer) OnLocalCandidate(func([]byte)) {}

// Transport は開いていない。Connector は「まだ開いていない」と読んで待つ。
func (p *fakePeer) Transport(ctx context.Context) (netplay.Transport, error) {
	return nil, ctx.Err()
}

func (p *fakePeer) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closes++
	return nil
}

func (p *fakePeer) localCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.localCalls
}

// peerFactory は newPeer に渡された host 引数を記録する。
type peerFactory struct {
	mu    sync.Mutex
	hosts []bool
	peers []*fakePeer
}

func (pf *peerFactory) make(host bool) (netplay.RTCPeer, error) {
	pf.mu.Lock()
	defer pf.mu.Unlock()
	p := &fakePeer{host: host}
	pf.hosts = append(pf.hosts, host)
	pf.peers = append(pf.peers, p)
	return p, nil
}

func (pf *peerFactory) made() ([]bool, []*fakePeer) {
	pf.mu.Lock()
	defer pf.mu.Unlock()
	return append([]bool(nil), pf.hosts...), append([]*fakePeer(nil), pf.peers...)
}

// mustNotBlock は f を別 goroutine で走らせ、期限内に戻らなければ落とす。
//
// **ブロックの検査は有限時間で落ちる形に書くこと。** そのまま呼んで待ち続けると、
// go test の打ち切り（既定10分）まで何が起きているか分からない。
func mustNotBlock(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s がブロックした（2秒以内に戻らなかった）", what)
	}
}

// waitFor は cond が真になるまで待つ。別 goroutine の後始末を見るために使う。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s が2秒以内に起きなかった", what)
}

// fixedNow は時計を止める。締切での失敗をテストに混ぜないためである。
func fixedNow() func() time.Time {
	base := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return base }
}

// startFake は繋がった状態から始める Handle を作る。
func startFake(t *testing.T, host bool, room string, conn *fakeConn, pf *peerFactory) *Handle {
	t.Helper()
	h := startWith(
		func(context.Context) (signalConn, error) { return conn, nil },
		pf.make,
		fixedNow(),
		host, room,
	)
	// Dial は別 goroutine なので、結果が届くまで Update を回す。
	waitFor(t, "Dial の結果が届く", func() bool {
		h.Update()
		return h.dialDone
	})
	return h
}

// --- Next の契約（ここが壊れると全対戦が即座に失敗する） ---

// 届いていないとき Next は即座に false を返し、context のエラーを失敗にしない。
//
// **これが最も重要な性質である。** 通知が1つも溜まっていないフレームは
// 対戦のほとんどを占めるので、ここを失敗扱いにすると全対戦が即座に落ちる。
func TestNextReturnsFalseWhenNothingArrived(t *testing.T) {
	conn := &fakeConn{}
	b := &sigBridge{conn: conn}

	var ev netplay.SigEvent
	var ok bool
	mustNotBlock(t, "Next", func() { ev, ok = b.Next() })
	if ok {
		t.Fatalf("届いていないのに ok=true（%+v）", ev)
	}
	// SigEvent は []byte を持つので == で比べられない。欄ごとに見る。
	if ev.Kind != netplay.SigCreated || ev.Room != "" || ev.Code != "" || len(ev.Data) != 0 {
		t.Errorf("届いていないのに中身がある: %+v", ev)
	}
	conn.mu.Lock()
	dead := append([]bool(nil), conn.ctxDead...)
	conn.mu.Unlock()
	if len(dead) != 1 || !dead[0] {
		t.Errorf("Next へ渡した context が期限切れでない: %v", dead)
	}

	// 接続は生きている。次に届いた通知はそのまま渡さねばならない。
	// context.Canceled を「死」と読む実装だと、ここで受け取れなくなる。
	conn.push(sigclient.Event{Kind: sigclient.EventCreated, Room: "P2Z5BS"})
	mustNotBlock(t, "Next", func() { ev, ok = b.Next() })
	if !ok {
		t.Fatalf("届いているのに ok=false")
	}
	if ev.Kind != netplay.SigCreated || ev.Room != "P2Z5BS" {
		t.Errorf("通知が違う: %+v", ev)
	}
}

// 通知が届かないだけで対戦は失敗しない（Handle 全体での同じ性質）。
func TestUpdateDoesNotFailWhileNothingArrives(t *testing.T) {
	conn := &fakeConn{}
	pf := &peerFactory{}
	h := startFake(t, true, "", conn, pf)
	defer h.Close()

	mustNotBlock(t, "Update の連打", func() {
		for i := 0; i < 200; i++ {
			if s := h.Update(); s == netplay.StateFailed {
				t.Errorf("%d 回目の Update で失敗した: %s", i, h.FailReason())
				return
			}
		}
	})
	if got := h.State(); got != netplay.StateCreating {
		t.Errorf("State = %v, 期待 Creating", got)
	}
}

// ErrUnreadable の1通は捨てて次を見る。
func TestNextSkipsUnreadableAndDelivers(t *testing.T) {
	conn := &fakeConn{}
	for i := 0; i < 3; i++ {
		conn.pushErr(fmt.Errorf("%w: 壊れた JSON", sigclient.ErrUnreadable))
	}
	conn.push(sigclient.Event{Kind: sigclient.EventPeerJoined})
	b := &sigBridge{conn: conn}

	var ev netplay.SigEvent
	var ok bool
	mustNotBlock(t, "Next", func() { ev, ok = b.Next() })
	if !ok || ev.Kind != netplay.SigPeerJoined {
		t.Fatalf("読み飛ばした先の通知を受け取れない: ok=%v ev=%+v", ok, ev)
	}
	if got := conn.calls(); got != 4 {
		t.Errorf("Next の呼び出し回数 = %d, 期待 4", got)
	}
}

// **読み飛ばしには上限がある。** 上限が無いと、解釈できない通知が続いたときに
// 1フレームの中で回り続け、Update がブロックしたのと同じことになる。
func TestNextGivesUpAfterTooManyUnreadable(t *testing.T) {
	conn := &fakeConn{always: &queued{err: fmt.Errorf("%w: 未知の種別", sigclient.ErrUnreadable)}}
	b := &sigBridge{conn: conn}

	var ok bool
	mustNotBlock(t, "Next（解釈できない通知が続くとき）", func() { _, ok = b.Next() })
	if ok {
		t.Fatalf("読めない通知しか無いのに ok=true")
	}
	// **期待値を定数そのもので書いてはならない。** `maxUnreadableSkips` と比べると
	// 定数を 100000 に変える変異が素通りし、守っているのは「上限という名の変数が
	// 存在すること」だけになる。ブリーフが要求したのは「1フレームの中で回り続けない」
	// ことなので、1フレーム（16.6ms）で捌ける絶対値で固定する。
	const wantLimit = 64
	if maxUnreadableSkips != wantLimit {
		t.Fatalf("上限 = %d, 期待 %d（1フレームで捌ける件数を超えてはならない）", maxUnreadableSkips, wantLimit)
	}
	if got := conn.calls(); got != wantLimit {
		t.Errorf("Next の呼び出し回数 = %d, 期待 %d（上限で諦めること）", got, wantLimit)
	}
}

// 転送そのものの失敗は disconnected を1回だけ合成し、以後は黙る。
//
// 合成しないと Connector は30秒（相手待ちなら10分）の締切まで
// 原因の分からないまま待つ。何度も合成すると、失敗の理由が上書きされ続ける。
func TestTransportFailureSynthesizesDisconnectedOnce(t *testing.T) {
	conn := &fakeConn{always: &queued{err: errors.New("接続が切れた")}}
	b := &sigBridge{conn: conn}

	ev, ok := b.Next()
	if !ok {
		t.Fatalf("転送が壊れたのに何も知らせない")
	}
	if ev.Kind != netplay.SigError || ev.Code != "disconnected" {
		t.Fatalf("合成された通知が違う: %+v", ev)
	}
	after := conn.calls()
	for i := 0; i < 3; i++ {
		if ev, ok := b.Next(); ok {
			t.Fatalf("%d 回目でまた知らせた: %+v", i+2, ev)
		}
	}
	if got := conn.calls(); got != after {
		t.Errorf("死んだ接続をまだ読んでいる: 呼び出し回数 %d → %d", after, got)
	}
}

// 転送が壊れたら対戦は締切を待たずに失敗する（Handle 全体での同じ性質）。
func TestBrokenTransportFailsPromptly(t *testing.T) {
	conn := &fakeConn{always: &queued{err: errors.New("接続が切れた")}}
	pf := &peerFactory{}
	h := startFake(t, true, "", conn, pf)
	defer h.Close()

	// 1回目の Update は create を送るだけなので、2回目で通知を読む。
	for i := 0; i < 4; i++ {
		h.Update()
	}
	if got := h.State(); got != netplay.StateFailed {
		t.Fatalf("State = %v, 期待 Failed", got)
	}
	if h.FailReason() == "" {
		t.Fatal("失敗の理由が空である")
	}
	// 合成したコードは画面に出る日本語まで繋がっていること。
	// 「サーバがエラーを返しました」だと、切断がサーバの都合で断られたことに読め、
	// 原因の見当が正反対に付く。
	if !strings.Contains(h.FailReason(), "接続が切れました") {
		t.Errorf("切断が切断として読めない: %q", h.FailReason())
	}
}

// 知らない種別は捨てて次を見る（読めなかった1通と同じ扱い）。
func TestNextSkipsUnknownKind(t *testing.T) {
	conn := &fakeConn{}
	conn.push(sigclient.Event{Kind: sigclient.EventKind(99)})
	conn.push(sigclient.Event{Kind: sigclient.EventPeerLeft})
	b := &sigBridge{conn: conn}

	ev, ok := b.Next()
	if !ok || ev.Kind != netplay.SigPeerLeft {
		t.Fatalf("知らない種別を捨てて次へ進めていない: ok=%v ev=%+v", ok, ev)
	}
}

// --- 通知の写し取り ---

func TestConvertEvent(t *testing.T) {
	cases := []struct {
		in   sigclient.EventKind
		want netplay.SigKind
	}{
		{sigclient.EventCreated, netplay.SigCreated},
		{sigclient.EventJoined, netplay.SigJoined},
		{sigclient.EventPeerJoined, netplay.SigPeerJoined},
		{sigclient.EventSignal, netplay.SigSignal},
		{sigclient.EventPeerLeft, netplay.SigPeerLeft},
		{sigclient.EventError, netplay.SigError},
	}
	for _, c := range cases {
		got, ok := convertEvent(sigclient.Event{Kind: c.in})
		if !ok {
			t.Errorf("%v を写せない", c.in)
			continue
		}
		if got.Kind != c.want {
			t.Errorf("%v → %v, 期待 %v", c.in, got.Kind, c.want)
		}
	}
	if _, ok := convertEvent(sigclient.Event{Kind: sigclient.EventKind(42)}); ok {
		t.Error("知らない種別を写してしまった")
	}

	// 欄の中身も運ぶこと。signal の Data を落とすと SDP が届かない。
	in := sigclient.Event{
		Kind: sigclient.EventSignal,
		Room: "P2Z5BS",
		Code: "room-full",
		Data: json.RawMessage(`{"kind":"sdp","body":{}}`),
	}
	got, ok := convertEvent(in)
	if !ok {
		t.Fatal("signal を写せない")
	}
	if got.Room != in.Room || got.Code != in.Code || string(got.Data) != string(in.Data) {
		t.Errorf("欄が欠けている: %+v", got)
	}
}

// --- Send の契約 ---

// **Send に期限付きの context を渡してはならない。**
// sigclient の send は書き込み前に ctx.Done() を見て即座に失敗し、
// Connector はその失敗を StateFailed に直結させる。
func TestSendUsesBackgroundContext(t *testing.T) {
	conn := &fakeConn{}
	b := &sigBridge{conn: conn}

	if err := b.Send("create", "", nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := b.Send("join", "P2Z5BS", nil); err != nil {
		t.Fatalf("join: %v", err)
	}
	if err := b.Send("signal", "", map[string]string{"kind": "sdp"}); err != nil {
		t.Fatalf("signal: %v", err)
	}
	if err := b.Send("いない", "", nil); err == nil {
		t.Error("知らない要求を送れてしまった")
	}

	want := []sentReq{
		{action: "create"},
		{action: "join", room: "P2Z5BS"},
		{action: "signal", data: []byte(`{"kind":"sdp"}`)},
	}
	for i, w := range want {
		got, ok := conn.sentAt(i)
		if !ok {
			t.Fatalf("%d 番目の要求が送られていない", i)
		}
		if got.action != w.action || got.room != w.room || string(got.data) != string(w.data) {
			t.Errorf("%d 番目の要求 = %+v, 期待 %+v", i, got, w)
		}
		if got.ctxHasDone {
			t.Errorf("%s に期限付きの context を渡している（接続が1フレームの遅れで死ぬ）", got.action)
		}
	}
}

// --- Dial のまわり ---

// Dial が失敗したら Failed になり、理由が読める。
func TestDialFailureFails(t *testing.T) {
	pf := &peerFactory{}
	h := startWith(
		func(context.Context) (signalConn, error) { return nil, errors.New("名前を解決できない") },
		pf.make, fixedNow(), true, "",
	)
	defer h.Close()

	waitFor(t, "Dial の失敗が伝わる", func() bool {
		return h.Update() == netplay.StateFailed
	})
	if h.FailReason() == "" {
		t.Fatal("失敗の理由が空である")
	}
	if !strings.Contains(h.FailReason(), "名前を解決できない") {
		t.Errorf("理由に元のエラーが無い: %q", h.FailReason())
	}
	// 終着点なので、以後の Update で状態が動かないこと。
	for i := 0; i < 5; i++ {
		if got := h.Update(); got != netplay.StateFailed {
			t.Fatalf("失敗のあと状態が動いた: %v", got)
		}
	}
	if hosts, _ := pf.made(); len(hosts) != 0 {
		t.Errorf("Dial に失敗したのに Peer を作った: %v", hosts)
	}
}

// **Dial の途中で Close されたら、遅れて届いた接続も閉じること。**
// 閉じ忘れると WebSocket と受信 goroutine が居座る。
func TestCloseDuringDialClosesLateConnection(t *testing.T) {
	release := make(chan struct{})
	conn := &fakeConn{}
	pf := &peerFactory{}
	h := startWith(
		func(context.Context) (signalConn, error) {
			<-release
			return conn, nil
		},
		pf.make, fixedNow(), true, "",
	)

	mustNotBlock(t, "Dial 中の Update", func() {
		for i := 0; i < 10; i++ {
			if got := h.Update(); got != netplay.StateCreating {
				t.Errorf("Dial 中の State = %v, 期待 Creating", got)
				return
			}
		}
	})

	h.Close()
	close(release) // ここで初めて Dial が接続を返す

	waitFor(t, "遅れて届いた接続が閉じられる", func() bool { return conn.closeCount() > 0 })
}

// Dial が先に終わっていても、Close は緩衝に残った接続を引き取って閉じる。
func TestCloseAfterDialClosesBufferedConnection(t *testing.T) {
	conn := &fakeConn{}
	pf := &peerFactory{}
	h := startWith(
		func(context.Context) (signalConn, error) { return conn, nil },
		pf.make, fixedNow(), true, "",
	)
	// **Update を呼ばずに**結果が緩衝へ入るのを待つ。Connector はまだ無いので、
	// この接続を閉じられるのは Close の引き取りだけである。
	waitFor(t, "Dial の結果が緩衝に入る", func() bool { return len(h.dialed) == 1 })

	h.Close()
	waitFor(t, "緩衝に残った接続が閉じられる", func() bool { return conn.closeCount() > 0 })
}

// Connector まで進んだあとの Close は、そちら経由で接続を閉じる。
func TestCloseAfterConnectorClosesConnection(t *testing.T) {
	conn := &fakeConn{}
	pf := &peerFactory{}
	h := startFake(t, true, "", conn, pf)

	h.Close()
	if got := conn.closeCount(); got != 1 {
		t.Fatalf("接続の Close 回数 = %d, 期待 1", got)
	}
	// 二度呼んでも安全であること。
	h.Close()
	if got := conn.closeCount(); got != 1 {
		t.Errorf("二度目の Close で余計に閉じた: %d", got)
	}
}

// --- 役割と席 ---

// **ホストは offer を作る側、ゲストは answer を返す側である。**
// 取り違えるとゲストの接続が全滅する。
func TestPeerRole(t *testing.T) {
	if got := peerRole(true); got != rtc.RoleOfferer {
		t.Errorf("ホストの役割 = %v, 期待 RoleOfferer", got)
	}
	if got := peerRole(false); got != rtc.RoleAnswerer {
		t.Errorf("ゲストの役割 = %v, 期待 RoleAnswerer", got)
	}
}

// newPeer には host がそのまま渡ること（役割はこの引数だけで決まる）。
func TestNewPeerReceivesHostFlag(t *testing.T) {
	cases := []struct {
		name string
		host bool
		// wantLocalSDP はホストだけが自分から offer を作ることの検査。
		wantLocalSDP int
	}{
		{"ホスト", true, 1},
		{"ゲスト", false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			conn := &fakeConn{}
			pf := &peerFactory{}
			h := startFake(t, c.host, "P2Z5BS", conn, pf)
			defer h.Close()

			h.Update() // create / join を送る
			conn.push(sigclient.Event{Kind: sigclient.EventPeerJoined})
			h.Update()

			hosts, peers := pf.made()
			if len(hosts) != 1 {
				t.Fatalf("Peer の作成回数 = %d, 期待 1", len(hosts))
			}
			if hosts[0] != c.host {
				t.Errorf("newPeer に渡された host = %v, 期待 %v", hosts[0], c.host)
			}
			if got := peers[0].localCount(); got != c.wantLocalSDP {
				t.Errorf("自分の SDP を作った回数 = %d, 期待 %d", got, c.wantLocalSDP)
			}
		})
	}
}

// 席はホスト = P1、ゲスト = P2。食い違うと盤面が鏡像になり全対戦が board-mismatch になる。
func TestSeat(t *testing.T) {
	cases := []struct {
		host bool
		want rules.Player
	}{
		{true, rules.P1},
		{false, rules.P2},
	}
	for _, c := range cases {
		conn := &fakeConn{}
		pf := &peerFactory{}
		// Dial の途中（Connector がまだ無い）でも同じ答えであること。
		h := startWith(
			func(ctx context.Context) (signalConn, error) { <-ctx.Done(); return nil, ctx.Err() },
			pf.make, fixedNow(), c.host, "P2Z5BS",
		)
		if got := h.Seat(); got != c.want {
			t.Errorf("Dial 中の Seat(host=%v) = %v, 期待 %v", c.host, got, c.want)
		}
		h.Close()

		h2 := startFake(t, c.host, "P2Z5BS", conn, pf)
		if got := h2.Seat(); got != c.want {
			t.Errorf("接続後の Seat(host=%v) = %v, 期待 %v", c.host, got, c.want)
		}
		h2.Close()
	}
}

// --- 全体の流れ ---

// ホストは create を送り、created で部屋番号を得て相手待ちへ移る。
func TestHostCreatesRoom(t *testing.T) {
	conn := &fakeConn{}
	pf := &peerFactory{}
	h := startFake(t, true, "", conn, pf)
	defer h.Close()

	h.Update()
	req, ok := conn.sentAt(0)
	if !ok || req.action != "create" {
		t.Fatalf("create を送っていない: %+v", req)
	}
	if h.Room() != "" {
		t.Errorf("created の前に部屋番号がある: %q", h.Room())
	}

	conn.push(sigclient.Event{Kind: sigclient.EventCreated, Room: "P2Z5BS"})
	if got := h.Update(); got != netplay.StateWaitingPeer {
		t.Fatalf("State = %v, 期待 WaitingPeer", got)
	}
	if h.Room() != "P2Z5BS" {
		t.Errorf("Room = %q, 期待 \"P2Z5BS\"", h.Room())
	}
}

// ゲストは入力された部屋番号で join を送る。
func TestGuestJoinsRoom(t *testing.T) {
	conn := &fakeConn{}
	pf := &peerFactory{}
	h := startFake(t, false, "P2Z5BS", conn, pf)
	defer h.Close()

	if got := h.Room(); got != "P2Z5BS" {
		t.Errorf("Room = %q, 期待 \"P2Z5BS\"", got)
	}
	h.Update()
	req, ok := conn.sentAt(0)
	if !ok || req.action != "join" || req.room != "P2Z5BS" {
		t.Fatalf("join を正しく送っていない: %+v", req)
	}
	if got := h.State(); got != netplay.StateConnecting {
		t.Errorf("State = %v, 期待 Connecting", got)
	}
}

// サーバのエラー通知はそのまま失敗の理由になる。
func TestServerErrorFails(t *testing.T) {
	conn := &fakeConn{}
	pf := &peerFactory{}
	h := startFake(t, false, "P2Z5BS", conn, pf)
	defer h.Close()

	h.Update() // join
	conn.push(sigclient.Event{Kind: sigclient.EventError, Code: "room-not-found"})
	if got := h.Update(); got != netplay.StateFailed {
		t.Fatalf("State = %v, 期待 Failed", got)
	}
	if !strings.Contains(h.FailReason(), "room-not-found") {
		t.Errorf("理由にコードが無い: %q", h.FailReason())
	}
}

// **Update はどの状態でもブロックしない。**
func TestUpdateNeverBlocks(t *testing.T) {
	t.Run("Dial 中", func(t *testing.T) {
		pf := &peerFactory{}
		h := startWith(
			func(ctx context.Context) (signalConn, error) { <-ctx.Done(); return nil, ctx.Err() },
			pf.make, fixedNow(), true, "",
		)
		defer h.Close()
		mustNotBlock(t, "Dial 中の Update", func() {
			for i := 0; i < 500; i++ {
				h.Update()
			}
		})
	})

	t.Run("接続後", func(t *testing.T) {
		conn := &fakeConn{}
		pf := &peerFactory{}
		h := startFake(t, true, "", conn, pf)
		defer h.Close()
		conn.push(sigclient.Event{Kind: sigclient.EventCreated, Room: "P2Z5BS"})
		conn.push(sigclient.Event{Kind: sigclient.EventPeerJoined})
		mustNotBlock(t, "接続後の Update", func() {
			for i := 0; i < 500; i++ {
				h.Update()
			}
		})
	})

	t.Run("Close のあと", func(t *testing.T) {
		conn := &fakeConn{}
		pf := &peerFactory{}
		h := startFake(t, true, "", conn, pf)
		h.Close()
		mustNotBlock(t, "Close 後の Update", func() {
			for i := 0; i < 500; i++ {
				h.Update()
			}
		})
	})
}

// --- 配線のもう半分（本物の pion を使う） ---
//
// ここから下は `peerBridge`・`newRTCPeer` を本物の rtc.Peer ごと動かす。
// 偽物だけで済ませると、SDP と candidate の橋渡し（ブリーフ §3）が丸ごと
// 無防備になる。レビューが置いた変異5件（自分の candidate を渡さない／
// 空の SDP を返す／届いた candidate を捨てる／Transport が常に失敗する／
// SetRemote が JSON の失敗を飲む）は、どれも「オンライン対戦が成立しない」
// 水準でありながら、偽の Peer では1件も落ちなかった。
// **網は使わない。** pion はループバックすら要らず、ホスト候補だけで繋がる。

// **`newRTCPeer` が役割を本当に配線しているか。**
//
// `peerRole` が正しくても、その戻り値が `rtc.NewPeer` に渡らなければ意味が無い。
// 純関数だけを見る `TestPeerRole` は、そこが壊れても緑のままである
// （ブリーフが名指しした「ゲスト接続が全滅する」壊れ方がすり抜ける）。
func TestNewRTCPeerWiresTheRole(t *testing.T) {
	hp, err := newRTCPeer(true) // ホスト = offerer
	if err != nil {
		t.Fatalf("ホストの Peer を作れない: %v", err)
	}
	defer hp.Close()
	raw, err := hp.LocalDescription()
	if err != nil {
		t.Fatalf("ホストが offer を作れない: %v", err)
	}
	var d rtc.Description
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("ホストの SDP を読めない: %v", err)
	}
	if d.Type != "offer" {
		t.Errorf("ホストの SDP type = %q, 期待 offer", d.Type)
	}
	if d.SDP == "" {
		t.Error("ホストの SDP が空である")
	}

	gp, err := newRTCPeer(false) // ゲスト = answerer
	if err != nil {
		t.Fatalf("ゲストの Peer を作れない: %v", err)
	}
	defer gp.Close()
	// answerer は遠隔記述が無ければ SDP を作れない。作れたら offerer になっている。
	if _, err := gp.LocalDescription(); err == nil {
		t.Error("ゲストが相手の SDP 無しで SDP を作れた（＝offerer になっている）")
	}
	if err := gp.SetRemote(raw); err != nil {
		t.Fatalf("ゲストが offer を受け取れない: %v", err)
	}
	araw, err := gp.LocalDescription()
	if err != nil {
		t.Fatalf("ゲストが answer を作れない: %v", err)
	}
	var ad rtc.Description
	if err := json.Unmarshal(araw, &ad); err != nil {
		t.Fatalf("ゲストの SDP を読めない: %v", err)
	}
	if ad.Type != "answer" {
		t.Errorf("ゲストの SDP type = %q, 期待 answer", ad.Type)
	}
	if err := hp.SetRemote(araw); err != nil {
		t.Fatalf("ホストが answer を受け取れない: %v", err)
	}

	// 自分の candidate が JSON になって上位へ届くこと。
	// ここを落とすと trickle が死に、ICE はまず繋がらない。
	got := make(chan []byte, 8)
	hp.OnLocalCandidate(func(b []byte) {
		select {
		case got <- b:
		default:
		}
	})
	select {
	case b := <-got:
		var c rtc.Candidate
		if err := json.Unmarshal(b, &c); err != nil {
			t.Fatalf("candidate が JSON になっていない: %v（%q）", err, b)
		}
		if c.Candidate == "" {
			t.Errorf("candidate が空である: %q", b)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("自分の candidate が上位へ渡ってこない")
	}
}

// 偽のシグナリング hub。create / join / signal を2者の間で中継するだけで、
// 本物のサーバ（第2段階a）の綴りだけを写してある。**網は使わない。**
type fakeHub struct {
	mu    sync.Mutex
	room  string
	sides [2]*hubConn
}

type hubConn struct {
	hub *fakeHub
	idx int

	mu    sync.Mutex
	inbox []sigclient.Event
}

func newFakeHub() *fakeHub {
	h := &fakeHub{}
	h.sides[0] = &hubConn{hub: h, idx: 0}
	h.sides[1] = &hubConn{hub: h, idx: 1}
	return h
}

func (h *fakeHub) deliver(idx int, e sigclient.Event) {
	c := h.sides[idx]
	c.mu.Lock()
	c.inbox = append(c.inbox, e)
	c.mu.Unlock()
}

func (c *hubConn) Create(context.Context) error {
	c.hub.mu.Lock()
	c.hub.room = "P2Z5BS"
	room := c.hub.room
	c.hub.mu.Unlock()
	c.hub.deliver(c.idx, sigclient.Event{Kind: sigclient.EventCreated, Room: room})
	return nil
}

func (c *hubConn) Join(_ context.Context, room string) error {
	c.hub.mu.Lock()
	ok := c.hub.room == room
	c.hub.mu.Unlock()
	if !ok {
		c.hub.deliver(c.idx, sigclient.Event{Kind: sigclient.EventError, Code: "room-not-found"})
		return nil
	}
	c.hub.deliver(c.idx, sigclient.Event{Kind: sigclient.EventJoined, Room: room})
	c.hub.deliver(1-c.idx, sigclient.Event{Kind: sigclient.EventPeerJoined, Room: room})
	return nil
}

func (c *hubConn) SendSignal(_ context.Context, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	c.hub.deliver(1-c.idx, sigclient.Event{Kind: sigclient.EventSignal, Data: raw})
	return nil
}

func (c *hubConn) Next(ctx context.Context) (sigclient.Event, error) {
	c.mu.Lock()
	if len(c.inbox) > 0 {
		e := c.inbox[0]
		c.inbox = c.inbox[1:]
		c.mu.Unlock()
		return e, nil
	}
	c.mu.Unlock()
	// 本物と同じ。届いていなければ呼び出し側の context が切れるまで待つ。
	<-ctx.Done()
	return sigclient.Event{}, ctx.Err()
}

func (c *hubConn) Close() error { return nil }

// **偽の hub に本物の WebRTC を繋ぎ、Update() だけで対戦を成立させる。**
//
// 9b が書くはずのコード（NewPlayStateWithSeat に Opponent と Seat を渡す）を
// そのまま書いてある。ここが通らなければオンライン対戦は成立しない。
// 偽の Peer では守れない性質を4つまとめて守る:
// 自分の candidate を相手へ渡すこと・空でない SDP を作ること・
// 届いた candidate を Peer へ渡すこと・Transport を素通しすること。
func TestRealPeersReachReadyAndPlayAMatch(t *testing.T) {
	hub := newFakeHub()
	hostH := startWith(
		func(context.Context) (signalConn, error) { return hub.sides[0], nil },
		newRTCPeer, nil, true, "",
	)
	defer hostH.Close()

	// ホストが部屋番号を得るまで回す。ゲストはその番号で入る（人が読み上げる手順）。
	deadline := time.Now().Add(20 * time.Second)
	for hostH.Room() == "" && time.Now().Before(deadline) {
		hostH.Update()
		time.Sleep(time.Millisecond)
	}
	if hostH.Room() != "P2Z5BS" {
		t.Fatalf("部屋番号を得られない: %q（%v %s）", hostH.Room(), hostH.State(), hostH.FailReason())
	}
	guestH := startWith(
		func(context.Context) (signalConn, error) { return hub.sides[1], nil },
		newRTCPeer, nil, false, hostH.Room(),
	)
	defer guestH.Close()

	if hostH.Opponent() != nil || guestH.Opponent() != nil {
		t.Error("Ready より前に Opponent が居る")
	}

	deadline = time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		hs, gs := hostH.Update(), guestH.Update()
		if hs == netplay.StateFailed {
			t.Fatalf("ホストが失敗した: %s", hostH.FailReason())
		}
		if gs == netplay.StateFailed {
			t.Fatalf("ゲストが失敗した: %s", guestH.FailReason())
		}
		if hs == netplay.StateReady && gs == netplay.StateReady {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if hostH.State() != netplay.StateReady || guestH.State() != netplay.StateReady {
		t.Fatalf("開通しなかった: ホスト=%v ゲスト=%v", hostH.State(), guestH.State())
	}
	if hostH.Seat() != rules.P1 || guestH.Seat() != rules.P2 {
		t.Fatalf("席が違う: ホスト=%v ゲスト=%v", hostH.Seat(), guestH.Seat())
	}
	if hostH.Opponent() == nil || guestH.Opponent() == nil {
		t.Fatal("Ready なのに Opponent が nil である")
	}

	// **開いた経路で実際に対戦を通す。** Transport が別物にすり替わっていたり、
	// 届いた通信が捨てられていたりすると、ここで揃わずに止まる。
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	hostPS := ui.NewPlayStateWithSeat(cfg, hostH.Opponent(), hostH.Seat())
	guestPS := ui.NewPlayStateWithSeat(cfg, guestH.Opponent(), guestH.Seat())
	moves := [2][]rules.Circle{
		{{CX: 0.3, CY: 0.3, R: 0.2}, {CX: 0.5, CY: 0.5, R: 0.15}, {CX: 0.2, CY: 0.7, R: 0.25}},
		{{CX: 0.7, CY: 0.7, R: 0.18}, {CX: 0.4, CY: 0.6, R: 0.3}, {CX: 0.8, CY: 0.2, R: 0.12}},
	}
	done := [2]map[int]bool{{}, {}}
	step := func(i int, s *ui.PlayState) {
		turn := s.Match().Turn()
		if s.Phase() == ui.PhaseDrawing && !done[i][turn] && turn < len(moves[i]) {
			s.SetPreview(moves[i][turn])
			_ = s.Confirm()
			done[i][turn] = true
		}
		s.Tick(1.0) // リビール演出を1フレームで終わらせる
	}
	deadline = time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if hostPS.Phase() == ui.PhaseFinished && guestPS.Phase() == ui.PhaseFinished {
			break
		}
		step(0, hostPS)
		step(1, guestPS)
		time.Sleep(time.Millisecond)
	}
	for _, p := range []struct {
		name string
		s    *ui.PlayState
	}{{"ホスト", hostPS}, {"ゲスト", guestPS}} {
		if p.s.Phase() != ui.PhaseFinished {
			t.Fatalf("%sの対戦が終わらなかった: %v（%s）", p.name, p.s.Phase(), p.s.AbortReason())
		}
		if p.s.AbortReason() != "" {
			t.Fatalf("%sが中断した: %s", p.name, p.s.AbortReason())
		}
	}
	if hr, gr := hostPS.Match().Result(), guestPS.Match().Result(); hr != gr {
		t.Errorf("勝敗が食い違う: ホスト=%v ゲスト=%v（席か盤面が鏡像になっている）", hr, gr)
	}
}

// **相手が壊れた SDP を1通送っても対戦は死んではならない。**
//
// `{}` も知らない綴りも JSON としては読めてしまうので、種別まで見ないと
// pion 側の一般のエラーになり、接続の死と見分けがつかなくなる。
func TestBadRemoteSDPIsBadSignal(t *testing.T) {
	cases := []struct {
		name string
		body string
		// want は誤りが**どの層で**見つかったかを示す文言。空なら層を問わない。
		// 種別の検査が、pion へ渡す前に自分で弾いていることの証拠として見る。
		//
		// **この検査は将来への備えではなく、今まさに効いている守りである。**
		// 3状態 × 2種別 × 3種類の SDP 本体を本物の pion で測った結果:
		// 本体が**本物の answer である pranswer** は、ホストが offer を送った直後の
		// 状態（have-local-offer）で**受け入れられる**（エラーが nil で返る）。
		// 中身の無い pranswer（"" や "v=0\r\n"）や rollback は拒まれるので、
		// 中身の無いもので測ると「どの種別も拒まれる」という誤った結論に至る。
		// 外したときに何が起きるかは TestPranswerDoesNotStealTheSDPSlot が
		// 振る舞いで固定している（中継された pranswer 1通が Connector の
		// remoteSDP を立て、あとから届く本物の answer が「2通目は捨てる」ガードで
		// 失われる）。**消してよい死んだコードではない。**
		want string
	}{
		// JSON か種別の段で弾けるもの。
		{"JSON ではない", `"ちがう"`, "相手の SDP を読めない"},
		{"null", `null`, "知らない SDP の種別"},
		{"空の物体", `{}`, "知らない SDP の種別"},
		{"知らない種別", `{"type":"どっちでもない","sdp":"v=0"}`, "知らない SDP の種別"},
		{"rollback", `{"type":"rollback","sdp":""}`, "知らない SDP の種別"},
		{"pranswer", `{"type":"pranswer","sdp":"v=0\r\n"}`, "知らない SDP の種別"},
		// **JSON の失敗を飲む実装をここで殺す。** 種別だけが正しく埋まり
		// sdp の型が違う body は、飲むと {Type:"offer", SDP:""} として
		// pion へ渡り、ErrBadSignal ではない一般のエラー（対戦の死）に化ける。
		{"sdp の型が違う", `{"type":"offer","sdp":123}`, "相手の SDP を読めない"},

		// **ここから下が要点である。** JSON としても種別としても正しく、
		// SDP 本体だけが壊れている1通は、JSON と種別の検査を素通りして pion に
		// 届く。手で壊すなら真っ先に作る形がこれなので、ここを ErrBadSignal に
		// 倒しておかないと「壊れた SDP 1通で対戦が死ぬ」は直ったことにならない。
		{"種別は正しいが SDP がゴミ", `{"type":"offer","sdp":"これは SDP ではない"}`, ""},
		{"種別は正しいが SDP が空", `{"type":"offer","sdp":""}`, ""},
		{"SDP が途中で切れている", "{\"type\":\"offer\",\"sdp\":\"v=0\\r\\no=- 1 1 IN IP4 0.0.0.0\\r\\n\"}", ""},
		{"行が壊れている", "{\"type\":\"offer\",\"sdp\":\"v=0\\r\\nax\\r\\n\"}", ""},
		// 相手が手順を無視して answer を先に送りつけてくる場合。
		{"answer を先に送りつける", "{\"type\":\"answer\",\"sdp\":\"v=0\\r\\n\"}", ""},
	}
	for _, c := range cases {
		// 事例ごとに新しい Peer を使う。1つを使い回すと、前の1通が状態を
		// 動かしたせいで落ちたのか、その1通自体が悪いのかが混ざる。
		p, err := newRTCPeer(false)
		if err != nil {
			t.Fatalf("%s: Peer を作れない: %v", c.name, err)
		}
		err = p.SetRemote([]byte(c.body))
		p.Close()
		if err == nil {
			t.Errorf("%s: 壊れた SDP を受け付けた", c.name)
			continue
		}
		if !errors.Is(err, netplay.ErrBadSignal) {
			t.Errorf("%s: ErrBadSignal を包んでいない（この1通で対戦が死ぬ）: %v", c.name, err)
			continue
		}
		if c.want != "" && !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %q で弾いていない（pion へ渡してしまっている）: %v", c.name, c.want, err)
		}
	}

	// 壊れた candidate も同じ扱いであること。
	p, err := newRTCPeer(false)
	if err != nil {
		t.Fatalf("Peer を作れない: %v", err)
	}
	defer p.Close()
	if err := p.AddCandidate([]byte(`ちがう`)); !errors.Is(err, netplay.ErrBadSignal) {
		t.Errorf("壊れた candidate が ErrBadSignal を包んでいない: %v", err)
	}

	// **正しい SDP まで拒んでは元も子もない。** 本物の offer は通ること。
	hp, err := newRTCPeer(true)
	if err != nil {
		t.Fatalf("ホストの Peer を作れない: %v", err)
	}
	defer hp.Close()
	offer, err := hp.LocalDescription()
	if err != nil {
		t.Fatalf("offer を作れない: %v", err)
	}
	gp, err := newRTCPeer(false)
	if err != nil {
		t.Fatalf("ゲストの Peer を作れない: %v", err)
	}
	defer gp.Close()
	if err := gp.SetRemote(offer); err != nil {
		t.Fatalf("正しい offer を拒んだ: %v", err)
	}
}

// 同じ性質を Handle 全体で。壊れた SDP の1通で StateFailed になってはならない。
func TestBadRemoteSDPDoesNotKillTheConnection(t *testing.T) {
	cases := []struct{ name, body string }{
		{"空の物体", `{}`},
		{"種別は正しいが SDP がゴミ", `{"type":"offer","sdp":"これは SDP ではない"}`},
		{"種別は正しいが SDP が空", `{"type":"offer","sdp":""}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			conn := &fakeConn{}
			h := startWith(
				func(context.Context) (signalConn, error) { return conn, nil },
				newRTCPeer, fixedNow(), false, "P2Z5BS",
			)
			defer h.Close()
			waitFor(t, "Dial の結果が届く", func() bool { h.Update(); return h.dialDone })

			h.Update() // join
			conn.push(sigclient.Event{Kind: sigclient.EventPeerJoined})
			h.Update()
			env, err := json.Marshal(map[string]any{"kind": "sdp", "body": json.RawMessage(c.body)})
			if err != nil {
				t.Fatal(err)
			}
			conn.push(sigclient.Event{Kind: sigclient.EventSignal, Data: env})
			if got := h.Update(); got == netplay.StateFailed {
				t.Fatalf("壊れた SDP 1通で対戦が死んだ: %s", h.FailReason())
			}
			if got := h.State(); got != netplay.StateConnecting {
				t.Errorf("State = %v, 期待 Connecting", got)
			}
		})
	}
}

// --- Start と Dial の後始末 ---

// **Dial に失敗したら、インタフェースとして nil を返すこと。**
//
// sigclient.Dial は失敗時に (*sigclient.Client)(nil) を返す。そのまま
// インタフェースへ入れると「型は入っているが値は nil」になり、
// Dial 中に Close された経路の nil 検査をすり抜けて panic する。
// URL の綴りだけで失敗するものを使うので、網には一切触れない。
func TestDialSignalingReturnsNilInterfaceOnFailure(t *testing.T) {
	conn, err := dialSignaling(context.Background(), "ftp://例.invalid/prod")
	if err == nil {
		t.Fatal("綴りの違う URL で失敗しない")
	}
	if conn != nil {
		t.Fatalf("失敗したのに nil でない接続を返した: %T", conn)
	}
	// 実際の後始末と同じことをして、落ちないことを見る。
	if conn != nil {
		conn.Close()
	}
}

// Start は本物の Dial を使う。繋がらない URL でも panic せず Failed になる。
func TestStartFailsCleanlyOnBadURL(t *testing.T) {
	h := Start("ftp://例.invalid/prod", true, "")
	defer h.Close()
	if got := h.State(); got != netplay.StateCreating {
		t.Errorf("Start 直後の State = %v, 期待 Creating", got)
	}
	waitFor(t, "Dial の失敗が伝わる", func() bool { return h.Update() == netplay.StateFailed })
	if h.FailReason() == "" {
		t.Error("失敗の理由が空である")
	}
	h.Close() // 遅れて届く接続の後始末で落ちないこと
}

// **Close は進行中の Dial を打ち切る。**
//
// 打ち切らないと、サーバが無応答のときの WebSocket ハンドシェイクが
// OS の TCP タイムアウトまで生き残る。取り消したのに繋ぎ続けることになる。
func TestCloseCancelsInFlightDial(t *testing.T) {
	cancelled := make(chan struct{})
	h := startWith(
		func(ctx context.Context) (signalConn, error) {
			<-ctx.Done()
			close(cancelled)
			return nil, ctx.Err()
		},
		(&peerFactory{}).make, fixedNow(), true, "",
	)
	h.Update()
	h.Close()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("Close が進行中の Dial を打ち切らなかった")
	}
}

// 動かせる時計。打ち切りの受け渡しを検査するために使う。
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// **Handle は自分の時計を Connector へ渡す。**
//
// 渡し忘れても NewConnectorWith が time.Now で埋めるので、普通のテストでは
// 何も起きない。しかし打ち切りが偽の時計で動かせなくなり、以後この層で
// 締切まわりを検査できなくなる。
func TestHandlePassesItsClockToTheConnector(t *testing.T) {
	clk := &testClock{t: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)}
	conn := &fakeConn{}
	pf := &peerFactory{}
	h := startWith(
		func(context.Context) (signalConn, error) { return conn, nil },
		pf.make, clk.now, true, "",
	)
	defer h.Close()
	waitFor(t, "Dial の結果が届く", func() bool { h.Update(); return h.dialDone })

	h.Update() // create 送信
	if got := h.Update(); got != netplay.StateCreating {
		t.Fatalf("State = %v, 期待 Creating", got)
	}
	clk.advance(netplay.ConnectTimeout + time.Second)
	if got := h.Update(); got != netplay.StateFailed {
		t.Fatalf("時計を進めても State = %v, 期待 Failed（時計が渡っていない）", got)
	}
	if !strings.Contains(h.FailReason(), "部屋を作れませんでした") {
		t.Errorf("打ち切りの理由が違う: %q", h.FailReason())
	}
}

// recordingPeer は peerBridge を包み、SetRemote に渡された種別と、実際に
// 適用された（＝失敗しなかった）種別を記録する。
type recordingPeer struct {
	netplay.RTCPeer
	mu      sync.Mutex
	passed  []string
	applied []string
}

func (r *recordingPeer) SetRemote(sdp []byte) error {
	var d rtc.Description
	_ = json.Unmarshal(sdp, &d)
	r.mu.Lock()
	r.passed = append(r.passed, d.Type)
	r.mu.Unlock()
	err := r.RTCPeer.SetRemote(sdp)
	if err == nil {
		r.mu.Lock()
		r.applied = append(r.applied, d.Type)
		r.mu.Unlock()
	}
	return err
}

func (r *recordingPeer) seen() (passed, applied []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.passed...), append([]string(nil), r.applied...)
}

// sdpSignal は「相手から中継された SDP 1通」の通知を組み立てる。
func sdpSignal(t *testing.T, body []byte) sigclient.Event {
	t.Helper()
	env, err := json.Marshal(map[string]any{"kind": "sdp", "body": json.RawMessage(body)})
	if err != nil {
		t.Fatalf("通知を組み立てられない: %v", err)
	}
	return sigclient.Event{Kind: sigclient.EventSignal, Data: env}
}

// lastSDPSent は自分が最後に中継してもらった SDP の本体を取り出す。
func lastSDPSent(t *testing.T, c *fakeConn) []byte {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.sends) - 1; i >= 0; i-- {
		if c.sends[i].action != "signal" {
			continue
		}
		var env struct {
			Kind string          `json:"kind"`
			Body json.RawMessage `json:"body"`
		}
		if err := json.Unmarshal(c.sends[i].data, &env); err != nil || env.Kind != "sdp" {
			continue
		}
		return env.Body
	}
	t.Fatal("SDP を1通も送っていない")
	return nil
}

// **SDP の種別検査は「今まさに効いている守り」である。**
//
// 中継された pranswer 1通で、あとから届く本物の answer を締め出せてはならない。
// 検査を外すと (1) pion が pranswer を受け入れ、(2) Connector が remoteSDP を立て、
// (3) 本物の answer が「2通目の SDP は捨てる」ガードで SetRemote に渡らなくなる。
// 交渉は止まったまま30秒の締切で死ぬ——所見4で塞いだ「1通で対戦を潰せる」穴が、
// 種別検査を外すと別の入口から開く。
//
// 文言（ErrBadSignal の中身）ではなく**振る舞い**で固定するのがこのテストの役目である。
// ICE も網も使わない。
func TestPranswerDoesNotStealTheSDPSlot(t *testing.T) {
	conn := &fakeConn{}
	rec := &recordingPeer{}
	h := startWith(
		func(context.Context) (signalConn, error) { return conn, nil },
		func(host bool) (netplay.RTCPeer, error) {
			p, err := newRTCPeer(host)
			if err != nil {
				return nil, err
			}
			rec.RTCPeer = p
			return rec, nil
		},
		fixedNow(), true, "",
	)
	defer h.Close()
	waitFor(t, "Dial の結果が届く", func() bool { h.Update(); return h.dialDone })

	h.Update() // create
	conn.push(sigclient.Event{Kind: sigclient.EventCreated, Room: "P2Z5BS"})
	h.Update()
	conn.push(sigclient.Event{Kind: sigclient.EventPeerJoined})
	h.Update() // Peer を作り offer を送る（ここで have-local-offer になる）

	offer := lastSDPSent(t, conn)

	// 相手役を1つ立てて、本物の answer を作らせる。
	gp, err := newRTCPeer(false)
	if err != nil {
		t.Fatalf("相手役の Peer を作れない: %v", err)
	}
	defer gp.Close()
	if err := gp.SetRemote(offer); err != nil {
		t.Fatalf("相手役が offer を受け取れない: %v", err)
	}
	answer, err := gp.LocalDescription()
	if err != nil {
		t.Fatalf("相手役が answer を作れない: %v", err)
	}
	var ad rtc.Description
	if err := json.Unmarshal(answer, &ad); err != nil {
		t.Fatalf("answer を読めない: %v", err)
	}

	// 毒。**本物の answer の SDP を積んだ pranswer は pion に受け入れられる**
	// （have-local-offer での実測）。中身の無い pranswer では毒にならない。
	poison, err := json.Marshal(rtc.Description{Type: "pranswer", SDP: ad.SDP})
	if err != nil {
		t.Fatal(err)
	}
	conn.push(sdpSignal(t, poison))
	if got := h.Update(); got == netplay.StateFailed {
		t.Fatalf("pranswer 1通で対戦が死んだ: %s", h.FailReason())
	}

	// そのあとに本物の answer が届く。
	conn.push(sdpSignal(t, answer))
	h.Update()

	passed, applied := rec.seen()
	if !slices.Contains(applied, "answer") {
		t.Fatalf("本物の answer が Peer へ届いていない（pranswer 1通が唯一の枠を奪った）: "+
			"SetRemote に渡された種別=%v 適用された種別=%v", passed, applied)
	}
	if slices.Contains(applied, "pranswer") {
		t.Errorf("pranswer を適用してしまった: 適用された種別=%v", applied)
	}
}

// TestUIPhaseCoversEveryConnectState は、接続の状態が漏れなく画面の語彙へ
// 写ることを守る。
//
// **この変換は netwire にしか置けない。** internal/netplay は ErrPeerGone と
// PeerFault のために internal/ui を import しているので依存の向きは
// netplay → ui の一方向であり、ui から netplay.ConnectState は見えない。
// gfx に置くとこの開発環境ではテストが1行も書けない。だからここに置き、
// ここで固定する。
//
// 漏れると default に落ちて ConnIdle になり、画面は部屋番号の入力欄を出す。
// つまり「接続中なのに入力欄が出て、押せるボタンが増える」形で壊れる。
func TestUIPhaseCoversEveryConnectState(t *testing.T) {
	cases := []struct {
		st   netplay.ConnectState
		want ui.ConnPhase
	}{
		{netplay.StateIdle, ui.ConnIdle},
		{netplay.StateCreating, ui.ConnCreating},
		{netplay.StateWaitingPeer, ui.ConnWaitingPeer},
		{netplay.StateConnecting, ui.ConnConnecting},
		{netplay.StateReady, ui.ConnReady},
		{netplay.StateFailed, ui.ConnFailed},
	}
	for _, c := range cases {
		if got := UIPhase(c.st); got != c.want {
			t.Errorf("UIPhase(%v) = %v, 期待 %v", c.st, got, c.want)
		}
	}

	// 単射であること。2つの状態が同じ表示に潰れると、画面は
	// 「部屋を作成中」と「接続中」を区別できなくなる。
	seen := map[ui.ConnPhase]netplay.ConnectState{}
	for _, c := range cases {
		got := UIPhase(c.st)
		if prev, dup := seen[got]; dup {
			t.Errorf("%v と %v がどちらも %v になる", prev, c.st, got)
		}
		seen[got] = c.st
	}

	// 知らない状態は ConnIdle に落とす（表示を持たない値を作らない）。
	if got := UIPhase(netplay.ConnectState(99)); got != ui.ConnIdle {
		t.Errorf("知らない状態 = %v, 期待 ConnIdle", got)
	}
}

// TestHandlePhaseFollowsState は Handle.Phase() が State() を素通しすること。
// 別々の判断を持つと、表示だけが1状態遅れる。
//
// **Dial 中と接続後の両方を見ること。** Handle は Connector ができるまでを
// h.state で、できたあとを Connector で答える（netwire.go の「できたあとは
// Connector が正典」）。h.state は startWith で一度だけ立てられ、
// adopt の失敗経路以外では二度と更新されない。だから Phase() が
// State() ではなく h.state を見ていても、Dial 中しか通らないテストは
// 素通りしてしまう。その退行が入ると、ホストの画面は「部屋を作成中…」から
// 永久に動かず、部屋番号も「接続中…」も失敗の理由も出ない
// （画面の上でオンライン対戦が全滅する）。同じファイルの TestSeat が
// 両方を見ているのと同じ形にしてある。
func TestHandlePhaseFollowsState(t *testing.T) {
	// --- Connector がまだ無い（Dial 中）---
	hostH := startWith(blockingDial, nil, time.Now, true, "")
	defer hostH.Close()
	if got, want := hostH.Phase(), UIPhase(hostH.State()); got != want {
		t.Errorf("ホスト: Dial 中の Phase() = %v, UIPhase(State()) = %v", got, want)
	}
	if hostH.Phase() != ui.ConnCreating {
		t.Errorf("部屋を作る側の初期表示 = %v, 期待 ConnCreating", hostH.Phase())
	}

	guestH := startWith(blockingDial, nil, time.Now, false, "P2Z5BS")
	defer guestH.Close()
	if got, want := guestH.Phase(), UIPhase(guestH.State()); got != want {
		t.Errorf("ゲスト: Dial 中の Phase() = %v, UIPhase(State()) = %v", got, want)
	}
	if guestH.Phase() != ui.ConnConnecting {
		t.Errorf("参加する側の初期表示 = %v, 期待 ConnConnecting", guestH.Phase())
	}

	// --- Connector ができ、状態が Dial 中の値から動いたあと ---
	conn := &fakeConn{}
	h := startFake(t, true, "", conn, &peerFactory{})
	defer h.Close()

	h.Update() // create を送る
	conn.push(sigclient.Event{Kind: sigclient.EventCreated, Room: "P2Z5BS"})
	if got := h.Update(); got != netplay.StateWaitingPeer {
		t.Fatalf("created を受けたのに State = %v, 期待 WaitingPeer", got)
	}
	// ここが要点。h.state は今も StateCreating のままなので、Phase() が
	// Connector を見ていなければ ConnCreating が返る。
	if got := h.Phase(); got != ui.ConnWaitingPeer {
		t.Errorf("接続後の Phase() = %v, 期待 ConnWaitingPeer（State() は %v）", got, h.State())
	}
	if got, want := h.Phase(), UIPhase(h.State()); got != want {
		t.Errorf("接続後: Phase() = %v, UIPhase(State()) = %v", got, want)
	}

	// 失敗まで進めても素通しであること（終着点も見る）。
	conn.pushErr(errors.New("転送が死んだ"))
	for i := 0; i < 8 && h.State() != netplay.StateFailed; i++ {
		h.Update()
	}
	if h.State() != netplay.StateFailed {
		t.Fatalf("失敗まで進まなかった: State = %v", h.State())
	}
	if got := h.Phase(); got != ui.ConnFailed {
		t.Errorf("失敗後の Phase() = %v, 期待 ConnFailed", got)
	}
}

// blockingDial は決して終わらない Dial。Connector ができる前の状態を作る。
func blockingDial(ctx context.Context) (signalConn, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
