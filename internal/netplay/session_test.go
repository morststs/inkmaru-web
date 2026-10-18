package netplay

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/morststs/inkmaru-web/internal/rules"
)

func newPair(t *testing.T) (*Session, *Session, func()) {
	t.Helper()
	ta, tb := NewPipe()
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	a := NewSession(ta, clock)
	b := NewSession(tb, clock)
	return a, b, func() { a.Close(); b.Close() }
}

// Poll を交互に回して両者の手が揃うまで進める。
func settle(t *testing.T, a, b *Session, turn int) (rules.Circle, rules.Circle) {
	t.Helper()
	var ca, cb rules.Circle
	var oka, okb bool
	for i := 0; i < 200 && (!oka || !okb); i++ {
		if !oka {
			c, ok, err := a.Poll(turn)
			if err != nil {
				t.Fatalf("a.Poll: %v", err)
			}
			ca, oka = c, ok
		}
		if !okb {
			c, ok, err := b.Poll(turn)
			if err != nil {
				t.Fatalf("b.Poll: %v", err)
			}
			cb, okb = c, ok
		}
	}
	if !oka || !okb {
		t.Fatalf("手が揃わなかった (a=%v b=%v)", oka, okb)
	}
	return ca, cb
}

func TestOneTurnCompletes(t *testing.T) {
	a, b, done := newPair(t)
	defer done()

	myA := rules.Circle{CX: 0.3, CY: 0.3, R: 0.2}
	myB := rules.Circle{CX: 0.7, CY: 0.7, R: 0.15}
	if err := a.Submit(myA, 0); err != nil {
		t.Fatalf("a.Submit: %v", err)
	}
	if err := b.Submit(myB, 0); err != nil {
		t.Fatalf("b.Submit: %v", err)
	}

	gotB, gotA := settle(t, a, b, 0)
	if gotB != myB {
		t.Errorf("a が受け取った相手の手 = %+v, 期待 %+v", gotB, myB)
	}
	if gotA != myA {
		t.Errorf("b が受け取った相手の手 = %+v, 期待 %+v", gotA, myA)
	}
}

// Poll は届いていなければ待たずに false を返す。ゲームループを止めないため。
func TestPollReturnsImmediatelyWhenNothingArrived(t *testing.T) {
	a, _, done := newPair(t)
	defer done()
	a.Submit(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}, 0)

	start := time.Now()
	_, ok, err := a.Poll(0)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if ok {
		t.Error("相手が何も送っていないのに手が揃った")
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Error("Poll がブロックした")
	}
}

// 仕様書 §5.3。commit が揃う前に reveal を送ると後出しが成立する。
func TestRevealIsNotSentBeforeBothCommits(t *testing.T) {
	ta, tb := NewPipe()
	defer ta.Close()
	defer tb.Close()
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	a := NewSession(ta, now)
	defer a.Close()

	a.Submit(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}, 0)
	// 相手側の生の Transport から読み、commit だけが来ていることを確かめる。
	rctx, rcancel := ctxNow(t)
	defer rcancel()
	raw, err := tb.Receive(rctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	m := decodeMsg(t, raw)
	if m.T != MsgCommit {
		t.Fatalf("最初のメッセージ = %v, 期待 commit", m.T)
	}
	// commit を返していないので reveal は来ないはず。
	sctx, scancel := ctxNow(t)
	defer scancel()
	if _, err := tb.Receive(sctx); err == nil {
		t.Error("commit が揃う前に2通目が届いた（reveal の先出し）")
	}
}

// 仕様書 §7.3。照合を外しても通るテストにしないため、改竄で中断することを確かめる。
func TestTamperedRevealAborts(t *testing.T) {
	ta, tb := NewPipe()
	defer ta.Close()
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	a := NewSession(ta, now)
	defer a.Close()

	a.Submit(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}, 0)
	dctx, dcancel := ctxNow(t)
	tb.Receive(dctx) // a の commit を捨てる
	dcancel()

	// 嘘の commit を送り、それと合わない reveal を送る。
	sendMsg(t, tb, Message{T: MsgCommit, Turn: 0, Hash: "00" + "ff"})
	sendMsg(t, tb, Message{T: MsgReveal, Turn: 0, CX: 0.9, CY: 0.9, R: 0.3, Nonce: "aabb"})

	var err error
	for i := 0; i < 50 && err == nil; i++ {
		_, _, err = a.Poll(0)
	}
	var ab AbortError
	if !errors.As(err, &ab) || ab.Reason != ReasonHashMismatch {
		t.Fatalf("err = %v, 期待 AbortError{hash-mismatch}", err)
	}
}

// TestWrongTurnAborts はレビュー所見2を受けて分割した。相手が1手先行しただけの
// メッセージ（s.turn+1）はもはや protocol 違反ではなく保留の対象になったため
// （TestNextTurnCommitIsBuffered 参照）、ここでは「過去のターン」と
// 「1手先行の範囲を超えた未来（s.turn+2）」の2通りだけが中断することを確かめる。
func TestWrongTurnAborts(t *testing.T) {
	t.Run("past", func(t *testing.T) {
		ta, tb := NewPipe()
		defer ta.Close()
		now := func() time.Time { return time.Unix(1_700_000_000, 0) }
		a := NewSession(ta, now)
		defer a.Close()

		if err := a.Submit(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}, 1); err != nil {
			t.Fatalf("a.Submit: %v", err)
		}
		wctx, wcancel := ctxNow(t)
		tb.Receive(wctx)
		wcancel()
		sendMsg(t, tb, Message{T: MsgCommit, Turn: 0, Hash: "deadbeef"}) // 過去のターン

		var err error
		for i := 0; i < 50 && err == nil; i++ {
			_, _, err = a.Poll(1)
		}
		var ab AbortError
		if !errors.As(err, &ab) || ab.Reason != ReasonProtocol {
			t.Fatalf("err = %v, 期待 AbortError{protocol}", err)
		}
	})

	t.Run("farFuture", func(t *testing.T) {
		ta, tb := NewPipe()
		defer ta.Close()
		now := func() time.Time { return time.Unix(1_700_000_000, 0) }
		a := NewSession(ta, now)
		defer a.Close()

		if err := a.Submit(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}, 0); err != nil {
			t.Fatalf("a.Submit: %v", err)
		}
		wctx, wcancel := ctxNow(t)
		tb.Receive(wctx)
		wcancel()
		sendMsg(t, tb, Message{T: MsgCommit, Turn: 2, Hash: "deadbeef"}) // s.turn+2、1手先行を超える

		var err error
		for i := 0; i < 50 && err == nil; i++ {
			_, _, err = a.Poll(0)
		}
		var ab AbortError
		if !errors.As(err, &ab) || ab.Reason != ReasonProtocol {
			t.Fatalf("err = %v, 期待 AbortError{protocol}", err)
		}
	})
}

// レビュー所見2。仕様書 §9.1 の「到着順が保たれない」という実測はシグナリング
// 経路についてのものであり、対戦本体が流れる DataChannel は ordered + reliable
// である（設計仕様書・task-3-review.md (a) 参照）。したがって相手は必ず
// board(t) の後に commit(t+1) を送るが、両方が自分の受信キューに同居した瞬間、
// 以前の実装は正しく振る舞う相手を protocol 違反として中断させていた。
// commit(t+1) は捨てずに保留し、自分が turn t+1 へ進んだときに使うべきである。
func TestNextTurnCommitIsBuffered(t *testing.T) {
	a, b, done := newPair(t)
	defer done()

	myA0 := rules.Circle{CX: 0.1, CY: 0.1, R: 0.1}
	myB0 := rules.Circle{CX: 0.2, CY: 0.2, R: 0.1}
	if err := a.Submit(myA0, 0); err != nil {
		t.Fatalf("a.Submit(0): %v", err)
	}
	if err := b.Submit(myB0, 0); err != nil {
		t.Fatalf("b.Submit(0): %v", err)
	}
	settle(t, a, b, 0)

	// b は先に turn 1 へ進むが、a はまだ turn 0 のつもりでいる
	// （盤面交換の途中を模す）。
	myB1 := rules.Circle{CX: 0.4, CY: 0.4, R: 0.12}
	if err := b.Submit(myB1, 1); err != nil {
		t.Fatalf("b.Submit(1): %v", err)
	}

	// a に commit(turn=1) が届くが、a はまだ turn 0 のつもりである。
	// 中断せず保留されるだけであることを確かめる。
	if _, _, err := a.Poll(0); err != nil {
		t.Fatalf("a.Poll(0) が保留すべき commit(1) で中断した: %v", err)
	}

	// a が turn 1 へ進む。保留されていた commit(1) がここで有効になり、
	// reveal の往復も揃うはず。
	myA1 := rules.Circle{CX: 0.3, CY: 0.3, R: 0.12}
	if err := a.Submit(myA1, 1); err != nil {
		t.Fatalf("a.Submit(1): %v", err)
	}
	gotB1, gotA1 := settle(t, a, b, 1)
	if gotB1 != myB1 {
		t.Errorf("a が受け取った turn1 の相手の手 = %+v, 期待 %+v", gotB1, myB1)
	}
	if gotA1 != myA1 {
		t.Errorf("b が受け取った turn1 の相手の手 = %+v, 期待 %+v", gotA1, myA1)
	}
}

// レビュー所見1（致命的）。ブリーフの maybeReveal の guard は「相手の commit が
// 届いたか」しか見ておらず、その commit が後から差し替え不能であることを
// 見ていなかった。攻撃手順をそのまま再現する。
//
//  1. 攻撃者は何もコミットしていないのに、適当な commit を送る。
//  2. 被害者 a は「相手の commit が届いた」と誤認し、自分の円を reveal する。
//  3. 攻撃者は a の commit と reveal を読み、a の円を正確に知る。
//  4. 攻撃者は a の円を見てから選んだ反撃円で、2通目の commit を送る。
//
// 2通目の commit は拘束力の無い後出しの試みなので、protocol 違反として
// 拒否されなければならない。
func TestSecondCommitAborts(t *testing.T) {
	ta, tb := NewPipe()
	defer ta.Close()
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	a := NewSession(ta, now)
	defer a.Close()

	// 1. 攻撃者は何もコミットしていないのに、適当な commit を送る。
	sendMsg(t, tb, Message{T: MsgCommit, Turn: 0, Hash: "deadbeef"})

	if err := a.Submit(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}, 0); err != nil {
		t.Fatalf("a.Submit: %v", err)
	}

	// 2. a は「相手の commit が届いた」と誤認し、自分の円を reveal してしまう。
	if _, _, err := a.Poll(0); err != nil {
		t.Fatalf("a.Poll: %v", err)
	}

	// 3. 攻撃者は a の commit と reveal を読み、a の円を正確に知る。
	rctx1, rcancel1 := ctxNow(t)
	tb.Receive(rctx1) // a の commit
	rcancel1()
	rctx2, rcancel2 := ctxNow(t)
	tb.Receive(rctx2) // a の reveal（本来ならここで a の円が漏れる）
	rcancel2()

	// 4. 攻撃者は a の円を見てから選んだ反撃円で、2通目の commit を送る。
	sendMsg(t, tb, Message{T: MsgCommit, Turn: 0, Hash: "cafef00d"})

	var err error
	for i := 0; i < 50 && err == nil; i++ {
		_, _, err = a.Poll(0)
	}
	var ab AbortError
	if !errors.As(err, &ab) || ab.Reason != ReasonProtocol {
		t.Fatalf("err = %v, 期待 AbortError{protocol}（2通目の commit）", err)
	}

	// 中断後、Poll をもう一度呼んでも ok=false のまま、err も AbortError のまま
	// であること（後出しされた円が正当な手として通ってはならない）。
	var ab2 AbortError
	if _, ok, polErr := a.Poll(0); ok {
		t.Errorf("中断後に手が揃ったことになっている (err=%v)", polErr)
	} else if !errors.As(polErr, &ab2) || ab2.Reason != ReasonProtocol {
		t.Errorf("中断後の Poll の err = %v, 期待 AbortError{protocol} のまま", polErr)
	}
}

// レビュー所見3。中断後は二度と勝敗（=有効な手）を確定させてはならない。
// TestTamperedRevealAborts は最初の Poll の err しか見ていなかったため、
// 「照合失敗後、2回目以降の Poll や ExchangeBoard が forged な値を返さない」
// ことをこのテストで固定する。
func TestAbortIsStickyAfterHashMismatch(t *testing.T) {
	ta, tb := NewPipe()
	defer ta.Close()
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	a := NewSession(ta, now)
	defer a.Close()

	a.Submit(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}, 0)
	dctx, dcancel := ctxNow(t)
	tb.Receive(dctx) // a の commit を捨てる
	dcancel()

	sendMsg(t, tb, Message{T: MsgCommit, Turn: 0, Hash: "00" + "ff"})
	sendMsg(t, tb, Message{T: MsgReveal, Turn: 0, CX: 0.9, CY: 0.9, R: 0.3, Nonce: "aabb"})

	var err error
	for i := 0; i < 50 && err == nil; i++ {
		_, _, err = a.Poll(0)
	}
	var ab AbortError
	if !errors.As(err, &ab) || ab.Reason != ReasonHashMismatch {
		t.Fatalf("err = %v, 期待 AbortError{hash-mismatch}", err)
	}

	// 中断後、何度 Poll しても偽造された円がそのまま正当な手として返らないこと。
	for i := 0; i < 3; i++ {
		c, ok, perr := a.Poll(0)
		if ok {
			t.Errorf("Poll #%d: 中断後なのに手が揃ったことになっている c=%+v", i, c)
		}
		var ab2 AbortError
		if !errors.As(perr, &ab2) || ab2.Reason != ReasonHashMismatch {
			t.Errorf("Poll #%d: err = %v, 期待 AbortError{hash-mismatch} が保持され続けること", i, perr)
		}
	}

	// ExchangeBoard も同様に中断状態を維持し続けること。
	cfg := rules.DefaultConfig()
	cfg.GridN = 32
	board := rules.NewBoard(cfg)
	okB, berr := a.ExchangeBoard(board, 0)
	if okB {
		t.Error("ExchangeBoard: 中断後なのに一致したことになっている")
	}
	var ab3 AbortError
	if !errors.As(berr, &ab3) || ab3.Reason != ReasonHashMismatch {
		t.Errorf("ExchangeBoard の err = %v, 期待 AbortError{hash-mismatch}", berr)
	}
}

// レビュー所見4。reveal が commit を追い越して届いた場合、それは順序の
// 入れ替わりであって、ハッシュが合わないという不正の断定はできない。
// commit 未着のまま reveal を受けたら hash-mismatch ではなく protocol で中断する。
func TestRevealBeforeCommitAbortsAsProtocol(t *testing.T) {
	ta, tb := NewPipe()
	defer ta.Close()
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	a := NewSession(ta, now)
	defer a.Close()

	if err := a.Submit(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}, 0); err != nil {
		t.Fatalf("a.Submit: %v", err)
	}
	// 相手の commit を待たず、reveal だけが先に届いた状況を作る。
	sendMsg(t, tb, Message{T: MsgReveal, Turn: 0, CX: 0.1, CY: 0.1, R: 0.1, Nonce: "aabb"})

	var err error
	for i := 0; i < 50 && err == nil; i++ {
		_, _, err = a.Poll(0)
	}
	var ab AbortError
	if !errors.As(err, &ab) || ab.Reason != ReasonProtocol {
		t.Fatalf("err = %v, 期待 AbortError{protocol}（commit 未着の reveal）", err)
	}
}

// レビュー所見6。相手が指定した中断理由を無検証で保持すると、Task 4 がそれを
// そのまま画面に表示するため、任意の文字列を相手の画面に出せてしまう。
// 既知の3つ以外は protocol に丸める。
func TestUnknownAbortReasonIsSanitizedToProtocol(t *testing.T) {
	ta, tb := NewPipe()
	defer ta.Close()
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	a := NewSession(ta, now)
	defer a.Close()

	if err := a.Submit(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}, 0); err != nil {
		t.Fatalf("a.Submit: %v", err)
	}
	sendMsg(t, tb, Message{T: MsgAbort, Reason: "あなたの不正が検出されました"})

	var err error
	for i := 0; i < 50 && err == nil; i++ {
		_, _, err = a.Poll(0)
	}
	var ab AbortError
	if !errors.As(err, &ab) {
		t.Fatalf("err = %v, 期待 AbortError", err)
	}
	if ab.Reason != ReasonProtocol {
		t.Errorf("ab.Reason = %q, 期待 %q（未知の reason は protocol に丸める）", ab.Reason, ReasonProtocol)
	}
}

// stubTransport はテスト用の Transport で、Receive の返り値を事前に指定できる。
// 所見5: 将来の Transport 実装（Task 6 の WebRTC ラッパ等）が期限切れに
// context.Canceled を返しても、drain がそれを致命的エラーと誤認しないことを
// 確かめるために使う。
type stubTransport struct {
	recv []func() ([]byte, error)
	i    int
}

func (s *stubTransport) Send(b []byte) error { return nil }

func (s *stubTransport) Receive(ctx context.Context) ([]byte, error) {
	if s.i >= len(s.recv) {
		return nil, context.DeadlineExceeded
	}
	f := s.recv[s.i]
	s.i++
	return f()
}

func (s *stubTransport) Close() error { return nil }

// レビュー所見5前半。drain は「何も届いていない」の判定を
// errors.Is(err, context.DeadlineExceeded) だけに頼っていたが、Transport の
// 実装によっては期限切れに context.Canceled を返しうる。どちらも
// 致命的エラーではなく「届いていないだけ」として扱われるべきである。
//
// Receive が途中で context.Canceled を返しつつ、同じ drain 呼び出し内で
// さらにメッセージを返すことは想定しない（それは transport.go の doc コメントが
// 定める「届いている分は締切の状態によらず優先して返す」契約に反する）。
// したがって drain の1回の呼び出しは「何も届かなかった」で終わってよく、
// 実際のメッセージは次のフレームの drain 呼び出し（＝毎フレームの Poll）で
// 届けば十分である。ここではその2段階を再現する。
func TestDrainToleratesContextCanceledFromTransport(t *testing.T) {
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	msg := Message{T: MsgBoard, Turn: 0, Hash: "abc"}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	st := &stubTransport{recv: []func() ([]byte, error){
		func() ([]byte, error) { return nil, context.Canceled },
	}}
	s := NewSession(st, now)
	defer s.Close()
	s.turn = 0 // board(turn=0) を受理させるため、直接ターンをそろえる

	// 1フレーム目: Receive が context.Canceled を返す。「届いていないだけ」
	// として扱われ、中断してはならない。
	if err := s.drain(); err != nil {
		t.Fatalf("drain がキャンセル済み context を実エラーと誤認して中断した: %v", err)
	}
	if s.aborted != nil {
		t.Fatalf("drain が context.Canceled だけで s.aborted を設定してしまった: %v", s.aborted)
	}

	// 2フレーム目相当: 実際に board が届く。
	st.recv = []func() ([]byte, error){
		func() ([]byte, error) { return raw, nil },
		func() ([]byte, error) { return nil, context.DeadlineExceeded },
	}
	st.i = 0
	if err := s.drain(); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if s.peerBoard != "abc" {
		t.Errorf("board が処理されていない: peerBoard=%q", s.peerBoard)
	}
}

// レビュー所見5後半（性能）。drain は毎回1msの受信期限を消費していたが、
// pipe.Receive は締切より先に非ブロッキングの peek をするため、期限を0にしても
// 既にキューにあるメッセージは変わらず届く。その契約を固定する。
func TestDrainDeliversAlreadyQueuedMessageWithExpiredContext(t *testing.T) {
	a, b, done := newPair(t)
	defer done()

	myA := rules.Circle{CX: 0.3, CY: 0.3, R: 0.2}
	myB := rules.Circle{CX: 0.7, CY: 0.7, R: 0.15}
	if err := a.Submit(myA, 0); err != nil {
		t.Fatalf("a.Submit: %v", err)
	}
	if err := b.Submit(myB, 0); err != nil {
		t.Fatalf("b.Submit: %v", err)
	}
	// b にいったん処理させ、b の commit と reveal を a の受信キューに積んでおく。
	if _, _, err := b.Poll(0); err != nil {
		t.Fatalf("b.Poll: %v", err)
	}

	if err := a.drain(); err != nil {
		t.Fatalf("a.drain: %v", err)
	}
	if !a.gotPeerReveal || a.peerCircle != myB {
		t.Errorf("drain がキュー済みの reveal を処理していない: gotPeerReveal=%v peerCircle=%+v", a.gotPeerReveal, a.peerCircle)
	}
}

func TestBoardMismatchAborts(t *testing.T) {
	a, b, done := newPair(t)
	defer done()

	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	ba := rules.NewBoard(cfg)
	ba.Apply(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}, rules.P1, cfg.InkPerTurn)
	bb := rules.NewBoard(cfg)
	bb.Apply(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}, rules.P2, cfg.InkPerTurn) // 所有者が違う

	var errA, errB error
	var okA, okB bool
	for i := 0; i < 200 && errA == nil && errB == nil && (!okA || !okB); i++ {
		if !okA {
			okA, errA = a.ExchangeBoard(ba, 0)
		}
		if !okB {
			okB, errB = b.ExchangeBoard(bb, 0)
		}
	}
	if errA == nil && errB == nil {
		t.Fatal("食い違う盤面なのに中断しなかった")
	}
	e := errA
	if e == nil {
		e = errB
	}
	var ab AbortError
	if !errors.As(e, &ab) || ab.Reason != ReasonBoardMismatch {
		t.Errorf("err = %v, 期待 AbortError{board-mismatch}", e)
	}
}

func TestMatchingBoardsPass(t *testing.T) {
	a, b, done := newPair(t)
	defer done()

	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	mk := func() *rules.Board {
		x := rules.NewBoard(cfg)
		x.Apply(rules.Circle{CX: 0.4, CY: 0.6, R: 0.18}, rules.P1, cfg.InkPerTurn)
		return x
	}
	var okA, okB bool
	for i := 0; i < 200 && (!okA || !okB); i++ {
		if !okA {
			var err error
			okA, err = a.ExchangeBoard(mk(), 0)
			if err != nil {
				t.Fatalf("a: %v", err)
			}
		}
		if !okB {
			var err error
			okB, err = b.ExchangeBoard(mk(), 0)
			if err != nil {
				t.Fatalf("b: %v", err)
			}
		}
	}
	if !okA || !okB {
		t.Error("一致する盤面なのに合意できなかった")
	}
}

func TestPeerGoneSurfaces(t *testing.T) {
	a, b, _ := newPair(t)
	defer a.Close()
	b.Close()

	a.Submit(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}, 0)
	var err error
	for i := 0; i < 50 && err == nil; i++ {
		_, _, err = a.Poll(0)
	}
	if !errors.Is(err, ErrPeerGone) {
		t.Errorf("err = %v, 期待 ErrPeerGone", err)
	}
}

// ctxNow は「少しだけ待つ」context を返す。呼び出し側が defer で cancel する。
// cancel を捨てると go vet の lostcancel に引っかかるため、必ず返して受け取る。
func ctxNow(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 50*time.Millisecond)
}

func sendMsg(t *testing.T, tr Transport, m Message) {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := tr.Send(b); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func decodeMsg(t *testing.T, b []byte) Message {
	t.Helper()
	var m Message
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	return m
}

// レビュー再検証（Fix round 2 所見1）。round 1 で「1手先行したメッセージは
// 捨てずに保留する」ようにした結果、悪意ある相手が turn+1 のメッセージを
// 際限なく送りつけて s.pending を無限に伸ばせるようになってしまった
// （round 1 より前は最初の1通で中断していたため、この問題は存在しなかった）。
// 正しく振る舞う相手が1ターンに送るのは commit・reveal・board の高々数通なので、
// pendingCap（8件）を明らかに超えたら中断する。
func TestPendingQueueCapTrips(t *testing.T) {
	ta, tb := NewPipe()
	defer ta.Close()
	defer tb.Close()
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	a := NewSession(ta, now)
	defer a.Close()

	if err := a.Submit(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}, 0); err != nil {
		t.Fatalf("a.Submit: %v", err)
	}

	// 上限を超える数の turn+1 メッセージを送りつける。
	for i := 0; i < pendingCap+1; i++ {
		sendMsg(t, tb, Message{T: MsgBoard, Turn: 1, Hash: "deadbeef"})
	}

	var err error
	for i := 0; i < 200 && err == nil; i++ {
		_, _, err = a.Poll(0)
	}
	var ab AbortError
	if !errors.As(err, &ab) || ab.Reason != ReasonProtocol {
		t.Fatalf("err = %v, 期待 AbortError{protocol}（保留上限超過）", err)
	}
}

// 正しく振る舞う相手が1ターン分に送る量（commit・reveal・board の3通）は
// 上限に対して十分小さく、誤って中断させられないことを確かめる。
func TestHonestTurnStaysUnderPendingCap(t *testing.T) {
	ta, tb := NewPipe()
	defer ta.Close()
	defer tb.Close()
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	a := NewSession(ta, now)
	defer a.Close()

	if err := a.Submit(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}, 0); err != nil {
		t.Fatalf("a.Submit: %v", err)
	}

	// 相手が1ターン先行して commit・reveal・board を1通ずつ送った状況を模す。
	sendMsg(t, tb, Message{T: MsgCommit, Turn: 1, Hash: "deadbeef"})
	sendMsg(t, tb, Message{T: MsgReveal, Turn: 1, CX: 0.1, CY: 0.1, R: 0.1, Nonce: "aabb"})
	sendMsg(t, tb, Message{T: MsgBoard, Turn: 1, Hash: "cafef00d"})

	if _, _, err := a.Poll(0); err != nil {
		t.Fatalf("a.Poll(0) が正常な1ターン分の先行メッセージで中断した: %v", err)
	}
	if got := len(a.pending); got != 3 {
		t.Errorf("len(a.pending) = %d, 期待 3（pendingCap=%d を大きく下回るはず）", got, pendingCap)
	}
}

// レビュー再検証（Fix round 2 所見2）。resetTurn はメッセージの再生中に
// abort に至ることがあるため error を返す設計になっているが、その戻り値を
// 呼び出し元が握りつぶすと、Submit は「内部的には中断済みなのに nil を返す」
// という危険な状態になる。呼び出し元がその場で中断を認識できることを固定する。
func TestSubmitSurfacesReplayAbort(t *testing.T) {
	ta, tb := NewPipe()
	defer ta.Close()
	defer tb.Close()
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	a := NewSession(ta, now)
	defer a.Close()

	a.turn = 0
	// 保留メッセージに、再生時に必ず中断するもの（空ハッシュの commit）を
	// 直接仕込む。
	a.pending = []Message{{T: MsgCommit, Turn: 1, Hash: ""}}

	err := a.Submit(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}, 1)
	var ab AbortError
	if !errors.As(err, &ab) || ab.Reason != ReasonProtocol {
		t.Fatalf("Submit の err = %v, 期待 AbortError{protocol}（保留の再生中の中断）", err)
	}
}

// 同上、ExchangeBoard 版。
func TestExchangeBoardSurfacesReplayAbort(t *testing.T) {
	ta, tb := NewPipe()
	defer ta.Close()
	defer tb.Close()
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	a := NewSession(ta, now)
	defer a.Close()

	a.turn = 0
	a.pending = []Message{{T: MsgCommit, Turn: 1, Hash: ""}}

	cfg := rules.DefaultConfig()
	cfg.GridN = 32
	board := rules.NewBoard(cfg)
	ok, err := a.ExchangeBoard(board, 1)
	if ok {
		t.Error("ExchangeBoard: 中断すべきなのに true が返った")
	}
	var ab AbortError
	if !errors.As(err, &ab) || ab.Reason != ReasonProtocol {
		t.Fatalf("ExchangeBoard の err = %v, 期待 AbortError{protocol}（保留の再生中の中断）", err)
	}
}

// レビュー再検証（Fix round 2 所見3・a）。round 1 で追加した「空ハッシュの
// commit を拒否する」ガードを直接確かめる。空ハッシュは相手が意図的に
// 送りうる（CommitHash が空文字列を返すことはないが、悪意ある相手は
// 生の JSON をどうとでも組み立てられる）ので、これは到達可能な保護である。
func TestEmptyHashCommitAborts(t *testing.T) {
	ta, tb := NewPipe()
	defer ta.Close()
	defer tb.Close()
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	a := NewSession(ta, now)
	defer a.Close()
	a.turn = 0

	err := a.handleMsg(Message{T: MsgCommit, Turn: 0, Hash: ""})
	var ab AbortError
	if !errors.As(err, &ab) || ab.Reason != ReasonProtocol {
		t.Fatalf("err = %v, 期待 AbortError{protocol}（空ハッシュの commit）", err)
	}
}

// レビュー再検証（Fix round 2 所見3・b）。round 1 で追加した「2度目の
// reveal を拒否する」ガードを直接確かめる。相手は commit を1通しか
// 送れなくなったが、reveal そのものを2回送ることは transport レベルでは
// 誰にも止められないので、これも到達可能な保護である。
func TestSecondRevealForSameTurnAborts(t *testing.T) {
	ta, tb := NewPipe()
	defer ta.Close()
	defer tb.Close()
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	a := NewSession(ta, now)
	defer a.Close()
	a.turn = 0
	a.peerCommit = "deadbeef" // 1通目の reveal は既に受理済みという状態を直接作る
	a.gotPeerReveal = true

	err := a.handleMsg(Message{T: MsgReveal, Turn: 0, CX: 0.1, CY: 0.1, R: 0.1, Nonce: "aabb"})
	var ab AbortError
	if !errors.As(err, &ab) || ab.Reason != ReasonProtocol {
		t.Fatalf("err = %v, 期待 AbortError{protocol}（2通目の reveal）", err)
	}
}

// 中断したことは相手にも届く。
//
// **これは「送っていること」にしか歯が無いテストである。** Session.abort の
// s.send(Message{T: MsgAbort, ...}) を丸ごと削っても、こちら側の結末は何も
// 変わらない。自分は aborted のまま終わり、相手も NetOpponent の相手待ちの
// 打ち切りで同じ結末（こちらの負け）に着地する。変わるのは着地までの時間
// （即時か30秒後か）だけなので、結果を比べるテストでは原理的に見えない。
// 仕様書 §4・§7.3 が名指しで要求している通信が、消えても誰にも気づかれない
// 状態だったので、ここで固定する。
func TestAbortIsAnnouncedToThePeer(t *testing.T) {
	ta, tb := NewPipe()
	defer ta.Close()
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	a := NewSession(ta, now)
	defer a.Close()

	if err := a.Submit(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}, 0); err != nil {
		t.Fatalf("a.Submit: %v", err)
	}
	// 嘘の commit と、それに合わない reveal を送りつけて a を中断させる。
	sendMsg(t, tb, Message{T: MsgCommit, Turn: 0, Hash: "00ff"})
	sendMsg(t, tb, Message{T: MsgReveal, Turn: 0, CX: 0.9, CY: 0.9, R: 0.3, Nonce: "aabb"})

	var err error
	for i := 0; i < 50 && err == nil; i++ {
		_, _, err = a.Poll(0)
	}
	var ab AbortError
	if !errors.As(err, &ab) || ab.Reason != ReasonHashMismatch {
		t.Fatalf("err = %v, 期待 AbortError{hash-mismatch}", err)
	}

	// 相手の側に届いたものを読み切って、abort が入っていることを確かめる。
	var got []MsgType
	for i := 0; i < 10; i++ {
		ctx, cancel := ctxNow(t)
		b, rerr := tb.Receive(ctx)
		cancel()
		if rerr != nil {
			break
		}
		m := decodeMsg(t, b)
		got = append(got, m.T)
		if m.T != MsgAbort {
			continue
		}
		if m.Reason != ReasonHashMismatch {
			t.Errorf("相手に届いた中断の理由 = %q, 期待 %q", m.Reason, ReasonHashMismatch)
		}
		return
	}
	t.Fatalf("中断したのに相手へ abort が届いていない（届いたのは %v）", got)
}

// 自分が居ないターンを Poll しても、相手の円は出てこない。
//
// **いまの配線では一度も発動しない守りである。** PlayState が Poll を呼ぶのは
// 必ず Confirm のあと（＝そのターンを Submit 済み）なので、ターン番号が
// 食い違うことがない。だから Poll 冒頭の `turn != s.turn` を削っても既存の
// テストは1件も落ちない。commit-reveal の第2防衛線を無検証のまま置かないために
// 固定する。将来 PhaseDrawing から Poll を呼ぶ変更が入れば、ここが落ちる。
func TestPollOnAnotherTurnRevealsNothing(t *testing.T) {
	a, b, done := newPair(t)
	defer done()

	if err := a.Submit(rules.Circle{CX: 0.3, CY: 0.3, R: 0.2}, 0); err != nil {
		t.Fatalf("a.Submit: %v", err)
	}
	if err := b.Submit(rules.Circle{CX: 0.7, CY: 0.7, R: 0.15}, 0); err != nil {
		t.Fatalf("b.Submit: %v", err)
	}
	// ターン0の手が両者に揃った状態を作る。a は相手の円を握っている。
	settle(t, a, b, 0)

	// まだ何も出していないターン1で、その円が漏れてはならない。
	for i := 0; i < 10; i++ {
		c, ok, err := a.Poll(1)
		if err != nil {
			t.Fatalf("a.Poll(1): %v", err)
		}
		if ok {
			t.Fatalf("居ないターンの Poll が相手の円を返した: %+v", c)
		}
	}
}

// 両者が再戦を申し出たら合意が成立し、セッションは対戦1の状態を捨てる。
func TestRematchAgreesWhenBothRequest(t *testing.T) {
	a, b, done := newPair(t)
	defer done()

	// 1ターンだけ済ませてから再戦に入る。
	if err := a.Submit(rules.Circle{CX: 0.3, CY: 0.3, R: 0.2}, 0); err != nil {
		t.Fatalf("a.Submit: %v", err)
	}
	if err := b.Submit(rules.Circle{CX: 0.7, CY: 0.7, R: 0.15}, 0); err != nil {
		t.Fatalf("b.Submit: %v", err)
	}
	settle(t, a, b, 0)

	if err := a.RequestRematch(0); err != nil {
		t.Fatalf("a.RequestRematch: %v", err)
	}
	// 片方だけでは成立しない。
	if ok, err := a.RematchAgreed(); err != nil || ok {
		t.Fatalf("片方だけで合意した: ok=%v err=%v", ok, err)
	}
	if err := b.RequestRematch(0); err != nil {
		t.Fatalf("b.RequestRematch: %v", err)
	}
	for i := 0; i < 50; i++ {
		oka, err := a.RematchAgreed()
		if err != nil {
			t.Fatalf("a.RematchAgreed: %v", err)
		}
		okb, err := b.RematchAgreed()
		if err != nil {
			t.Fatalf("b.RematchAgreed: %v", err)
		}
		if oka && okb {
			break
		}
		if i == 49 {
			t.Fatalf("合意が成立しなかった (a=%v b=%v)", oka, okb)
		}
	}

	// 対戦2のターン0が最初から使える＝対戦1の状態が残っていない。
	if err := a.Submit(rules.Circle{CX: 0.1, CY: 0.1, R: 0.1}, 0); err != nil {
		t.Fatalf("再戦後の a.Submit: %v", err)
	}
	if err := b.Submit(rules.Circle{CX: 0.9, CY: 0.9, R: 0.1}, 0); err != nil {
		t.Fatalf("再戦後の b.Submit: %v", err)
	}
	ca, cb := settle(t, a, b, 0)
	if ca.R != 0.1 || cb.R != 0.1 {
		t.Errorf("再戦後に届いた円が対戦1のもの: a が見た相手=%v b が見た相手=%v", ca, cb)
	}
}

// 対戦2のメッセージが自分の Submit より先に届いても壊れない（設計 §3）。
//
// 合意は送ったと読んだが揃った側から成立するので、相手が先に対戦2を始めることは
// 例外ではなく通常の順序である。ここでは b が先に押し、a がそれを読む前に押すので、
// 先に押した b のほうが先に合意して対戦2を始める。合意の瞬間に turn を -1 に戻すことで、
// 対戦2のターン0が s.turn+1 として緩衝され、こちらの Submit のときに再生される。
// 戻さないと、終わった対戦のターン番号と食い違って protocol 中断になる。
func TestNextMatchMessagesArrivingEarlyAreBuffered(t *testing.T) {
	a, b, done := newPair(t)
	defer done()

	if err := a.Submit(rules.Circle{CX: 0.3, CY: 0.3, R: 0.2}, 0); err != nil {
		t.Fatalf("a.Submit: %v", err)
	}
	if err := b.Submit(rules.Circle{CX: 0.7, CY: 0.7, R: 0.15}, 0); err != nil {
		t.Fatalf("b.Submit: %v", err)
	}
	settle(t, a, b, 0)

	// b が「再戦したい」と「対戦2のターン0の commit」を立て続けに送る。
	// a はまだ一度も読んでいないので、次の読み取りで両方が届いている。
	if err := b.RequestRematch(0); err != nil {
		t.Fatalf("b.RequestRematch: %v", err)
	}
	if ok, err := b.RematchAgreed(); err != nil || ok {
		t.Fatalf("b が独りで合意した: ok=%v err=%v", ok, err)
	}
	if err := a.RequestRematch(0); err != nil {
		t.Fatalf("a.RequestRematch: %v", err)
	}
	for i := 0; i < 50; i++ {
		ok, err := b.RematchAgreed()
		if err != nil {
			t.Fatalf("b.RematchAgreed: %v", err)
		}
		if ok {
			break
		}
	}
	if err := b.Submit(rules.Circle{CX: 0.9, CY: 0.9, R: 0.1}, 0); err != nil {
		t.Fatalf("再戦後の b.Submit: %v", err)
	}

	// a が読む。rematch と commit が同じキューに並んでいる。
	ok, err := a.RematchAgreed()
	if err != nil {
		t.Fatalf("a.RematchAgreed: %v", err)
	}
	if !ok {
		t.Fatal("a が合意を観測できなかった")
	}
	if err := a.Submit(rules.Circle{CX: 0.1, CY: 0.1, R: 0.1}, 0); err != nil {
		t.Fatalf("再戦後の a.Submit: %v（境界をまたいで読み進めた）", err)
	}
	ca, _ := settle(t, a, b, 0)
	if ca.R != 0.1 {
		t.Errorf("a が受け取った相手の円 = %v, 期待 R=0.1", ca)
	}
}

// 中断したセッションでは再戦を申し出られない（設計 §6）。
func TestRematchIsRefusedAfterAbort(t *testing.T) {
	a, b, done := newPair(t)
	defer done()

	if err := a.Submit(rules.Circle{CX: 0.3, CY: 0.3, R: 0.2}, 0); err != nil {
		t.Fatalf("a.Submit: %v", err)
	}
	// 相手が不正な reveal を送りつけ、a が hash-mismatch で中断する。
	raw, err := json.Marshal(Message{T: MsgCommit, Turn: 0, Hash: "deadbeef"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := b.tr.Send(raw); err != nil {
		t.Fatalf("Send: %v", err)
	}
	raw, err = json.Marshal(Message{T: MsgReveal, Turn: 0, CX: 0.5, CY: 0.5, R: 0.2, Nonce: "00"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := b.tr.Send(raw); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, _, err := a.Poll(0); err == nil {
		t.Fatal("改竄された reveal で中断しなかった")
	}

	if err := a.RequestRematch(0); err == nil {
		t.Fatal("中断したセッションが再戦を申し出た")
	}
	if ok, err := a.RematchAgreed(); ok || err == nil {
		t.Fatalf("中断したセッションが合意を返した: ok=%v err=%v", ok, err)
	}
}

// 相手が先に申し出ていた場合でも、自分の申し出はエラーを返さない。
//
// 合意の成立は drain を止める合図（errStopDrain）を内部で使うが、それが
// 呼び出し側まで漏れると「相手が先に押していたときだけ再戦に失敗する」という、
// 押す順番でしか再現しない欠陥になる。
func TestRequestRematchSucceedsWhenPeerAskedFirst(t *testing.T) {
	a, b, done := newPair(t)
	defer done()

	if err := b.RequestRematch(0); err != nil {
		t.Fatalf("b.RequestRematch: %v", err)
	}
	// a が相手の申し出を先に読んでしまう。
	if ok, err := a.RematchAgreed(); err != nil || ok {
		t.Fatalf("a が独りで合意した: ok=%v err=%v", ok, err)
	}
	// そのうえで a が押す。ここで内部の合図が漏れてはならない。
	if err := a.RequestRematch(0); err != nil {
		t.Fatalf("a.RequestRematch: %v（内部の合図が漏れている）", err)
	}
	ok, err := a.RematchAgreed()
	if err != nil {
		t.Fatalf("a.RematchAgreed: %v", err)
	}
	if !ok {
		t.Fatal("両者が押したのに合意していない")
	}
}

// 対戦1で緩衝していた「1手先行した相手」のメッセージは、再戦に持ち越さない（設計 §4）。
//
// resetTurn は pending を捨てずに再生する（それが緩衝の役目である）。だから
// 合意の時点で捨てないと、対戦1の未来ターンの commit が対戦2で再生され、
// 相手の本物の commit が来たときに「1ターンに2通目」として protocol 中断になる。
// 中断するのは正しい相手との対戦なので、再戦は必ず1ターン目か2ターン目で壊れる。
func TestStalePendingIsDroppedOnRematch(t *testing.T) {
	a, b, done := newPair(t)
	defer done()

	// 対戦1のターン0。b は1手先行してターン1の commit も撃ち込む。
	if err := a.Submit(rules.Circle{CX: 0.3, CY: 0.3, R: 0.2}, 0); err != nil {
		t.Fatalf("a.Submit: %v", err)
	}
	if err := b.Submit(rules.Circle{CX: 0.7, CY: 0.7, R: 0.15}, 0); err != nil {
		t.Fatalf("b.Submit: %v", err)
	}
	settle(t, a, b, 0)
	raw, err := json.Marshal(Message{T: MsgCommit, Turn: 1, Hash: "0011223344556677"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := b.tr.Send(raw); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, _, err := a.Poll(0); err != nil { // ここで pending に積まれる
		t.Fatalf("a.Poll: %v", err)
	}

	// 再戦する。
	if err := a.RequestRematch(0); err != nil {
		t.Fatalf("a.RequestRematch: %v", err)
	}
	if err := b.RequestRematch(0); err != nil {
		t.Fatalf("b.RequestRematch: %v", err)
	}
	for i := 0; i < 50; i++ {
		oka, err := a.RematchAgreed()
		if err != nil {
			t.Fatalf("a.RematchAgreed: %v", err)
		}
		okb, err := b.RematchAgreed()
		if err != nil {
			t.Fatalf("b.RematchAgreed: %v", err)
		}
		if oka && okb {
			break
		}
	}

	// 対戦2を2ターン進める。持ち越していれば、ターン1で相手の commit が
	// 2通目になって protocol 中断になる。
	for turn := 0; turn < 2; turn++ {
		if err := a.Submit(rules.Circle{CX: 0.2, CY: 0.2, R: 0.1}, turn); err != nil {
			t.Fatalf("対戦2 ターン%d の a.Submit: %v（対戦1の緩衝を持ち越している）", turn, err)
		}
		if err := b.Submit(rules.Circle{CX: 0.8, CY: 0.8, R: 0.1}, turn); err != nil {
			t.Fatalf("対戦2 ターン%d の b.Submit: %v", turn, err)
		}
		settle(t, a, b, turn)
	}
}

// 1回の対戦につき rematch は1通だけ。2通目は protocol 違反である。
func TestSecondRematchAborts(t *testing.T) {
	a, b, done := newPair(t)
	defer done()

	raw, err := json.Marshal(Message{T: MsgRematch, Turn: 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := b.tr.Send(raw); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	if _, err := a.RematchAgreed(); err == nil {
		t.Fatal("2通目の rematch を受け入れた")
	} else {
		var ae AbortError
		if !errors.As(err, &ae) || ae.Reason != ReasonProtocol {
			t.Errorf("中断の理由 = %v, 期待 %s", err, ReasonProtocol)
		}
	}
}
