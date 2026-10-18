package netplay

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/morststs/inkmaru-web/internal/rules"
	"github.com/morststs/inkmaru-web/internal/ui"
)

func TestNetOpponentExchangesMoves(t *testing.T) {
	ta, tb := NewPipe()
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	a := NewNetOpponent(ta, now)
	b := NewNetOpponent(tb, now)
	defer a.Close()
	defer b.Close()

	myA := rules.Circle{CX: 0.2, CY: 0.2, R: 0.2}
	myB := rules.Circle{CX: 0.8, CY: 0.8, R: 0.1}
	a.Submit(myA, 0)
	b.Submit(myB, 0)

	var gotA, gotB rules.Circle
	var okA, okB bool
	for i := 0; i < 200 && (!okA || !okB); i++ {
		if !okA {
			gotB, okA, _ = a.Poll(0)
		}
		if !okB {
			gotA, okB, _ = b.Poll(0)
		}
	}
	if !okA || !okB {
		t.Fatal("手が揃わなかった")
	}
	if gotB != myB || gotA != myA {
		t.Errorf("交換結果が違う: a が %v, b が %v", gotB, gotA)
	}
}

// 制限時間はマッチング中に減ってはならない。ICE の完了待ちや
// 「相手が見つかりました」の表示を挟むぶん、構築時点と対戦開始は一致しない。
func TestStartAnchorsTurnZero(t *testing.T) {
	ta, _ := NewPipe()
	base := time.Unix(1_700_000_000, 0)
	cur := base
	a := NewNetOpponent(ta, func() time.Time { return cur })
	defer a.Close()

	// ここで Deadline を参照しないこと。参照した時点で自己起動してしまい、
	// Start() が起点を動かすかどうかを見られなくなる（自己起動そのものは
	// TestDeadlineAnchorsOnFirstUseWhenStartForgotten で検査する）。
	cur = base.Add(20 * time.Second) // 接続に20秒かかった
	a.Start()
	got, ok := a.Deadline(0)
	if !ok {
		t.Fatal("Start したのに期限が立っていない")
	}
	if want := cur.Add(TurnLimit); !got.Equal(want) {
		t.Errorf("期限 = %v, 期待 %v（Start の時刻から30秒）", got, want)
	}

	// 2回目以降の Start は起点を動かさない。
	cur = base.Add(25 * time.Second)
	a.Start()
	if got2, _ := a.Deadline(0); !got2.Equal(got) {
		t.Errorf("2回目の Start で期限が %v から %v に動いた", got, got2)
	}
}

// 描画フェーズの起点は Submit や Poll では立たない。それらが最初に呼ばれるのは
// プレイヤーが確定したあとなので、起点にすると考えていた時間がまるごと
// 制限時間の外に出る。
//
// 起点が立っていないことは「最初に Deadline を参照した時刻から30秒」に
// なることで観測する（Start を呼び忘れたときの自己起動）。Submit が起点を
// 立てていれば、期限は Submit の時刻から30秒になって食い違う。
func TestSubmitAndPollDoNotAnchorTheTurn(t *testing.T) {
	ta, tb := NewPipe()
	base := time.Unix(1_700_000_000, 0)
	cur := base
	clock := func() time.Time { return cur }
	a := NewNetOpponent(ta, clock)
	peer := NewSession(tb, clock)
	defer a.Close()
	defer tb.Close()

	a.Submit(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2}, 0)
	peer.Submit(rules.Circle{CX: 0.8, CY: 0.8, R: 0.1}, 0)
	var ok bool
	for i := 0; i < 20 && !ok; i++ {
		peer.Poll(0)
		_, ok, _ = a.Poll(0)
	}
	if !ok {
		t.Fatal("相手の手が届かなかった") // 待ちを終わらせてから期限を見る
	}

	cur = base.Add(10 * time.Second)
	got, anchored := a.Deadline(0)
	if !anchored {
		t.Fatal("期限が立たなかった")
	}
	if want := cur.Add(TurnLimit); !got.Equal(want) {
		t.Errorf("期限 = %v, 期待 %v（Submit / Poll は起点を立てない）", got, want)
	}
}

// Poll は自分の手を提出してはならない（Ruling A）。wire に出る円を決める経路が
// PlayState.Confirm とここの2本になると、時間切れの自動提出が commit を先に
// 埋め、あとからプレイヤーが確定した円が sentCommit で黙って捨てられる。
// 自分の盤面と相手に届いた円が食い違って board-mismatch になり、防ごうとした
// 時間切れより悪い結末（両者とも中断）を作る。制限時間は PlayState が強制する。
func TestPollNeverSubmitsOnItsOwn(t *testing.T) {
	ta, tb := NewPipe()
	base := time.Unix(1_700_000_000, 0)
	cur := base
	a := NewNetOpponent(ta, func() time.Time { return cur })
	defer a.Close()
	defer tb.Close()
	a.Start()

	cur = base.Add(10 * TurnLimit) // 制限時間をはるかに超えている
	for i := 0; i < 100; i++ {
		if _, _, err := a.Poll(0); err != nil {
			t.Fatalf("Poll が失敗した: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	if raw, err := tb.Receive(ctx); err == nil {
		t.Errorf("Poll が勝手に %s を送った", raw)
	}
}

// 2ターン目以降の起点は「前のターンの盤面ハッシュが一致した瞬間」である。
// PlayState はそこで PhaseRevealing を抜けて次の描画フェーズに入る。
func TestDeadlineRestartsWhenBoardAgreed(t *testing.T) {
	ta, tb := NewPipe()
	base := time.Unix(1_700_000_000, 0)
	cur := base
	clock := func() time.Time { return cur }
	a := NewNetOpponent(ta, clock)
	b := NewNetOpponent(tb, clock)
	defer a.Close()
	defer b.Close()
	a.Start()
	b.Start()

	a.Submit(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2}, 0)
	b.Submit(rules.Circle{CX: 0.8, CY: 0.8, R: 0.1}, 0)
	for i := 0; i < 200; i++ {
		a.Poll(0)
		b.Poll(0)
	}

	if _, ok := a.Deadline(1); ok {
		t.Fatal("盤面を照合する前にターン1の起点が立っている")
	}

	cur = base.Add(12 * time.Second)
	board := rules.NewBoard(rules.DefaultConfig())
	var agreed bool
	for i := 0; i < 200 && !agreed; i++ {
		b.ConfirmBoard(board, 0)
		var err error
		agreed, err = a.ConfirmBoard(board, 0)
		if err != nil {
			t.Fatalf("ConfirmBoard が失敗した: %v", err)
		}
	}
	if !agreed {
		t.Fatal("盤面ハッシュが一致しなかった")
	}
	got, ok := a.Deadline(1)
	if !ok {
		t.Fatal("一致したのにターン1の起点が立っていない")
	}
	if want := cur.Add(TurnLimit); !got.Equal(want) {
		t.Errorf("ターン1の期限 = %v, 期待 %v", got, want)
	}
}

// ConfirmBoard は毎フレーム呼ばれる。ブロックせず、同じ盤面で何度呼ばれても
// 結果が変わってはならない。
func TestConfirmBoardIsRepeatableAndNonBlocking(t *testing.T) {
	ta, tb := NewPipe()
	base := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return base }
	a := NewNetOpponent(ta, clock)
	b := NewNetOpponent(tb, clock)
	defer a.Close()
	defer b.Close()

	a.Submit(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2}, 0)
	b.Submit(rules.Circle{CX: 0.8, CY: 0.8, R: 0.1}, 0)
	for i := 0; i < 200; i++ {
		a.Poll(0)
		b.Poll(0)
	}

	board := rules.NewBoard(rules.DefaultConfig())
	// 相手がまだ送っていない間は、何度呼んでも false・エラー無しで即座に戻る。
	// 実測は1回 0.2ms 程度。1秒は4桁の余裕がありながら、10秒眠る実装は捕まえる。
	started := time.Now()
	for i := 0; i < 5; i++ {
		agreed, err := a.ConfirmBoard(board, 0)
		if err != nil || agreed {
			t.Fatalf("%d 回目: agreed=%v err=%v, 期待 false・nil", i, agreed, err)
		}
	}
	if el := time.Since(started); el > time.Second {
		t.Errorf("待機中の ConfirmBoard 5回に %v かかった。ブロックしている", el)
	}
	b.ConfirmBoard(board, 0)
	for i := 0; i < 3; i++ {
		agreed, err := a.ConfirmBoard(board, 0)
		if err != nil || !agreed {
			t.Fatalf("一致後 %d 回目: agreed=%v err=%v, 期待 true・nil", i, agreed, err)
		}
	}
}

// 待機中は盤面ハッシュを1回だけ計算する。GridN=256 では1回 66KB の確保になり、
// 毎フレーム呼ばれる経路でそれを繰り返す理由が無い。
func TestConfirmBoardHashesTheBoardOnce(t *testing.T) {
	ta, _ := NewPipe()
	base := time.Unix(1_700_000_000, 0)
	a := NewNetOpponent(ta, func() time.Time { return base })
	defer a.Close()
	a.Submit(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2}, 0)

	board := rules.NewBoard(rules.DefaultConfig()) // GridN=256
	a.ConfirmBoard(board, 0)                       // 1回目でハッシュを控える

	const n = 100
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := 0; i < n; i++ {
		a.ConfirmBoard(board, 0)
	}
	runtime.ReadMemStats(&after)
	grew := after.TotalAlloc - before.TotalAlloc
	// 毎回ハッシュし直すと 100 回で 6.5MB を超える。控えていれば数十KB で済む。
	if grew > 2<<20 {
		t.Errorf("待機中の %d 回で %d バイト確保した。盤面を毎回ハッシュし直している", n, grew)
	}
}

// 切断は3経路すべてで ui.ErrPeerGone に翻訳しなければならない。
func TestSubmitTranslatesPeerGone(t *testing.T) {
	ta, tb := NewPipe()
	base := time.Unix(1_700_000_000, 0)
	a := NewNetOpponent(ta, func() time.Time { return base })
	tb.Close()

	err := a.Submit(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}, 0)
	if err == nil {
		t.Fatal("Submit がエラーを返さなかった")
	}
	if !errors.Is(err, ui.ErrPeerGone) {
		t.Errorf("Submit のエラー %v は ui.ErrPeerGone ではない", err)
	}
	if !errors.Is(err, ErrPeerGone) {
		t.Errorf("Submit のエラー %v は netplay.ErrPeerGone でもあるべき", err)
	}
}

func TestPollTranslatesPeerGone(t *testing.T) {
	// Receive が確実に ErrPeerGone を返す Transport を使う。NewPipe を閉じた場合、
	// 期限0の context と閉鎖の両方が同時に成立するため、どちらが返るかは
	// select の擬似ランダム選択に委ねられてフレークする。
	tr := &stubTransport{recv: []func() ([]byte, error){
		func() ([]byte, error) { return nil, ErrPeerGone },
	}}
	base := time.Unix(1_700_000_000, 0)
	a := NewNetOpponent(tr, func() time.Time { return base })
	defer a.Close()
	if err := a.Submit(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}, 0); err != nil {
		t.Fatalf("前提の Submit が失敗した: %v", err)
	}

	_, _, err := a.Poll(0)
	if err == nil {
		t.Fatal("Poll がエラーを返さなかった")
	}
	if !errors.Is(err, ui.ErrPeerGone) {
		t.Errorf("Poll のエラー %v は ui.ErrPeerGone ではない", err)
	}
	if !errors.Is(err, ErrPeerGone) {
		t.Errorf("Poll のエラー %v は netplay.ErrPeerGone でもあるべき", err)
	}
}

func TestConfirmBoardTranslatesPeerGone(t *testing.T) {
	ta, tb := NewPipe()
	base := time.Unix(1_700_000_000, 0)
	a := NewNetOpponent(ta, func() time.Time { return base })
	if err := a.Submit(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}, 0); err != nil {
		t.Fatalf("前提の Submit が失敗した: %v", err)
	}
	tb.Close()

	_, err := a.ConfirmBoard(rules.NewBoard(rules.DefaultConfig()), 0)
	if err == nil {
		t.Fatal("ConfirmBoard がエラーを返さなかった")
	}
	if !errors.Is(err, ui.ErrPeerGone) {
		t.Errorf("ConfirmBoard のエラー %v は ui.ErrPeerGone ではない", err)
	}
}

// 素の切断には理由が無い。結果画面には「あなたの勝ち」だけが出る。
func TestPlainPeerGoneCarriesNoReason(t *testing.T) {
	ta, tb := NewPipe()
	base := time.Unix(1_700_000_000, 0)
	a := NewNetOpponent(ta, func() time.Time { return base })
	tb.Close()

	err := a.Submit(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}, 0)
	var pf ui.PeerFault
	if !errors.As(err, &pf) {
		t.Fatalf("エラー %v が ui.PeerFault を満たしていない", err)
	}
	if pf.PeerFaultReason() != "" {
		t.Errorf("素の切断に理由 %q が付いている", pf.PeerFaultReason())
	}
}

// errTransport は Send が任意のエラーを返す Transport。自分側の失敗を作る。
type errTransport struct{ sendErr error }

func (e *errTransport) Send([]byte) error { return e.sendErr }
func (e *errTransport) Receive(ctx context.Context) ([]byte, error) {
	return nil, context.DeadlineExceeded
}
func (e *errTransport) Close() error { return nil }

// 自分側の失敗は翻訳しない。ここまで勝ちにすると、自分のバグで勝ってしまう。
func TestLocalFailureIsNotTranslated(t *testing.T) {
	own := errors.New("netplay: 送信の変換に失敗")
	base := time.Unix(1_700_000_000, 0)
	a := NewNetOpponent(&errTransport{sendErr: own}, func() time.Time { return base })
	defer a.Close()

	err := a.Submit(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}, 0)
	if !errors.Is(err, own) {
		t.Fatalf("エラー %v が元のエラーを保っていない", err)
	}
	if errors.Is(err, ui.ErrPeerGone) {
		t.Error("自分側の失敗を切断として翻訳している")
	}
}

// --- 通しの確認（本物の PlayState に本物の NetOpponent を差す） ---

// newLinkedState は PlayState に NetOpponent を差し、相手側の Transport を返す。
func newLinkedState(t *testing.T, clock func() time.Time) (*ui.PlayState, Transport, *NetOpponent) {
	t.Helper()
	ta, tb := NewPipe()
	o := NewNetOpponent(ta, clock)
	o.Start()
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	return ui.NewPlayStateWith(cfg, o), tb, o
}

func mustSend(t *testing.T, tr Transport, m Message) {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("変換に失敗: %v", err)
	}
	if err := tr.Send(b); err != nil {
		t.Fatalf("送信に失敗: %v", err)
	}
}

// 翻訳が本当に勝敗まで届くことを、3経路とも PlayState 越しに確かめる。
// 単体テストは NetOpponent の境界で止まるので、PlayState 側の分類器が
// 変わっても落ちない。守りたい性質は PeerLeft() が立つことである。
func TestPeerGoneReachesPlayStateOnAllThreePaths(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return base }
	mine := rules.Circle{CX: 0.2, CY: 0.2, R: 0.2}

	t.Run("Submit", func(t *testing.T) {
		s, tb, _ := newLinkedState(t, clock)
		tb.Close()
		s.SetPreview(mine)
		_ = s.Confirm()
		if !s.PeerLeft() {
			t.Errorf("PeerLeft = false, 期待 true（reason=%q）", s.AbortReason())
		}
	})

	t.Run("Poll", func(t *testing.T) {
		ta := &stubTransport{recv: []func() ([]byte, error){
			func() ([]byte, error) { return nil, ErrPeerGone },
		}}
		o := NewNetOpponent(ta, clock)
		o.Start()
		cfg := rules.DefaultConfig()
		cfg.GridN = 64
		s := ui.NewPlayStateWith(cfg, o)
		s.SetPreview(mine)
		if err := s.Confirm(); err != nil {
			t.Fatalf("Confirm: %v", err)
		}
		s.Tick(0.016)
		if !s.PeerLeft() {
			t.Errorf("PeerLeft = false, 期待 true（reason=%q）", s.AbortReason())
		}
	})

	t.Run("ConfirmBoard", func(t *testing.T) {
		s, tb, _ := newLinkedState(t, clock)
		peer := NewSession(tb, clock)
		s.SetPreview(mine)
		if err := s.Confirm(); err != nil {
			t.Fatalf("Confirm: %v", err)
		}
		peer.Submit(rules.Circle{CX: 0.8, CY: 0.8, R: 0.1}, 0)
		for i := 0; i < 20 && s.Phase() == ui.PhaseWaitingPeer; i++ {
			peer.Poll(0)
			s.Tick(0.016)
		}
		if s.Phase() != ui.PhaseRevealing {
			t.Fatalf("フェーズ = %v, 期待 PhaseRevealing", s.Phase())
		}
		tb.Close() // 盤面ハッシュを送る直前に相手が消える
		s.Tick(1.0)
		if !s.PeerLeft() {
			t.Errorf("PeerLeft = false, 期待 true（reason=%q）", s.AbortReason())
		}
	})
}

// 相手が中断を通告してきた場合も相手の負けである。
// 負けている側が abort を1通投げてから切断すれば確定した敗北を
// 引き分けに変えられる、という穴を塞ぐ（§5.4 の逃げ得防止）。
func TestPeerAssertedAbortScoresAsPeerLoss(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	s, tb, _ := newLinkedState(t, func() time.Time { return base })

	s.SetPreview(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2})
	if err := s.Confirm(); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	mustSend(t, tb, Message{T: MsgAbort, Turn: 0, Reason: ReasonHashMismatch})
	tb.Close()
	for i := 0; i < 10 && s.Phase() != ui.PhaseFinished; i++ {
		s.Tick(0.016)
	}

	if !s.PeerLeft() {
		t.Error("中断の通告で勝敗が消えた。負けている側が引き分けに逃げられる")
	}
	if s.AbortReason() == "" {
		t.Error("なぜ終わったのかが残っていない")
	}
}

// 自分で不正を検出した場合も相手の負けである。不正をした側が
// 引き分けを得てよい理由が無い。
func TestSelfDetectedFaultScoresAsPeerLoss(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	s, tb, _ := newLinkedState(t, func() time.Time { return base })

	s.SetPreview(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2})
	if err := s.Confirm(); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	// commit と、それに一致しない reveal を送りつける。
	mustSend(t, tb, Message{T: MsgCommit, Turn: 0, Hash: "00000000000000000000000000000000000000000000000000000000000000ff"})
	s.Tick(0.016)
	mustSend(t, tb, Message{
		T: MsgReveal, Turn: 0, CX: 0.8, CY: 0.8, R: 0.1,
		Nonce: hex.EncodeToString([]byte("0123456789abcdef")),
	})
	for i := 0; i < 10 && s.Phase() != ui.PhaseFinished; i++ {
		s.Tick(0.016)
	}

	if !s.PeerLeft() {
		t.Error("自分で検出した不正で勝敗が消えた。不正をした側が引き分けを得ている")
	}
	if s.AbortReason() == "" {
		t.Error("なぜ終わったのかが残っていない")
	}
}

// --- 相手待ちの打ち切り（Fix round 2） ---

// 相手が繋がっているのに手を出さない場合、こちらは永久に待たされる。
// 悪意がなくても半開きの接続で起こる。待ちにも同じ30秒を課し、
// 超えたら切断として扱う（＝Ruling B により自分の勝ち）。
func TestPeerWaitsAreCutOffAfterTurnLimit(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	mine := rules.Circle{CX: 0.2, CY: 0.2, R: 0.2}

	// 相手が commit すら送ってこない。
	t.Run("commitが来ない", func(t *testing.T) {
		ta, tb := NewPipe()
		cur := base
		a := NewNetOpponent(ta, func() time.Time { return cur })
		defer a.Close()
		defer tb.Close()
		a.Start()
		if err := a.Submit(mine, 0); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		assertWaitTimesOut(t, &cur, base, func() error {
			_, _, err := a.Poll(0)
			return err
		})
	})

	// 相手が commit だけ送って reveal を送ってこない。
	t.Run("revealが来ない", func(t *testing.T) {
		ta, tb := NewPipe()
		cur := base
		a := NewNetOpponent(ta, func() time.Time { return cur })
		defer a.Close()
		defer tb.Close()
		a.Start()
		if err := a.Submit(mine, 0); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		mustSend(t, tb, Message{T: MsgCommit, Turn: 0, Hash: "00000000000000000000000000000000000000000000000000000000000000ff"})
		if _, _, err := a.Poll(0); err != nil {
			t.Fatalf("commit の受理で失敗した: %v", err)
		}
		assertWaitTimesOut(t, &cur, base, func() error {
			_, _, err := a.Poll(0)
			return err
		})
	})

	// 相手が盤面ハッシュを送ってこない。
	t.Run("盤面ハッシュが来ない", func(t *testing.T) {
		ta, tb := NewPipe()
		cur := base
		clock := func() time.Time { return cur }
		a := NewNetOpponent(ta, clock)
		peer := NewSession(tb, clock)
		defer a.Close()
		defer tb.Close()
		a.Start()
		if err := a.Submit(mine, 0); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		peer.Submit(rules.Circle{CX: 0.8, CY: 0.8, R: 0.1}, 0)
		var ok bool
		for i := 0; i < 20 && !ok; i++ {
			peer.Poll(0)
			_, ok, _ = a.Poll(0)
		}
		if !ok {
			t.Fatal("相手の手が届かなかった")
		}
		board := rules.NewBoard(rules.DefaultConfig())
		assertWaitTimesOut(t, &cur, base, func() error {
			_, err := a.ConfirmBoard(board, 0)
			return err
		})
	})
}

// assertWaitTimesOut は、待ちが制限時間内はエラーにならず、超えたら
// 切断として打ち切られることを確かめる。時計は cur を書き換えて進める。
func assertWaitTimesOut(t *testing.T, cur *time.Time, base time.Time, call func() error) {
	t.Helper()
	// 待ちの起点は最初の呼び出しである。PlayState は待ちに入った瞬間から
	// 毎フレーム呼ぶので、まず base で1回呼んでから時計を進める。
	if err := call(); err != nil {
		t.Fatalf("待ち始めで打ち切られた: %v", err)
	}
	*cur = base.Add(TurnLimit - time.Second)
	for i := 0; i < 5; i++ {
		if err := call(); err != nil {
			t.Fatalf("制限時間内なのに打ち切られた: %v", err)
		}
	}
	*cur = base.Add(TurnLimit + time.Second)
	err := call()
	if err == nil {
		t.Fatal("制限時間を超えても待ち続けている（相手が黙れば永久に固まる）")
	}
	if !errors.Is(err, ui.ErrPeerGone) {
		t.Errorf("エラー %v が ui.ErrPeerGone ではない", err)
	}
	var pf ui.PeerFault
	if !errors.As(err, &pf) || pf.PeerFaultReason() == "" {
		t.Errorf("打ち切りの理由が残っていない: %v", err)
	}
	// 一度打ち切ったら戻らない。
	if err2 := call(); err2 == nil {
		t.Error("打ち切りのあとエラーが消えた")
	}
}

// 待ちの制限時間は待ちごとに数え直す。1ターンぶんの合計ではない。
// 相手が29秒で応答したのに、次の待ちが1秒しか残っていないのはおかしい。
func TestEachWaitGetsItsOwnLimit(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	cur := base
	clock := func() time.Time { return cur }
	ta, tb := NewPipe()
	a := NewNetOpponent(ta, clock)
	peer := NewSession(tb, clock)
	defer a.Close()
	defer tb.Close()
	a.Start()

	if err := a.Submit(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2}, 0); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	a.Poll(0) // ここで手の待ちが始まる

	cur = base.Add(TurnLimit - time.Second) // 相手はぎりぎりで応答する
	peer.Submit(rules.Circle{CX: 0.8, CY: 0.8, R: 0.1}, 0)
	var ok bool
	for i := 0; i < 20 && !ok; i++ {
		peer.Poll(0)
		var err error
		_, ok, err = a.Poll(0)
		if err != nil {
			t.Fatalf("ぎりぎりの応答で打ち切られた: %v", err)
		}
	}
	if !ok {
		t.Fatal("相手の手が届かなかった")
	}

	// 盤面ハッシュの待ちには新しい30秒がある。
	board := rules.NewBoard(rules.DefaultConfig())
	cur = cur.Add(20 * time.Second)
	if _, err := a.ConfirmBoard(board, 0); err != nil {
		t.Fatalf("盤面待ちが前の待ちの残りで打ち切られた: %v", err)
	}
}

// 相手待ちの間も残り時間が動く。0 に張り付いたままだと、プレイヤーには
// 固まったのか待っているのか区別がつかない。
func TestRemainingCountsDownWhileWaitingForPeer(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	cur := base
	s, tb, _ := newLinkedState(t, func() time.Time { return cur })
	defer tb.Close()

	s.SetPreview(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2})
	cur = base.Add(25 * time.Second) // 25秒考えてから確定した
	if err := s.Confirm(); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if s.Phase() != ui.PhaseWaitingPeer {
		t.Fatalf("フェーズ = %v, 期待 PhaseWaitingPeer", s.Phase())
	}

	// 描画の30秒はもう5秒しか残っていないが、相手待ちには新しい30秒がある。
	got, ok := s.Remaining()
	if !ok {
		t.Fatal("相手待ちの残り時間が取れない")
	}
	if got != TurnLimit {
		t.Errorf("待ち始めの残り時間 = %v, 期待 %v", got, TurnLimit)
	}
	cur = cur.Add(10 * time.Second)
	s.Tick(0.016)
	if got, _ := s.Remaining(); got != TurnLimit-10*time.Second {
		t.Errorf("10秒後の残り時間 = %v, 期待 %v", got, TurnLimit-10*time.Second)
	}
}

// 相手が黙り込んだら、対戦は固まらずに自分の勝ちで終わる。
func TestSilentPeerEndsTheMatchAsAWin(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	cur := base
	s, tb, _ := newLinkedState(t, func() time.Time { return cur })
	defer tb.Close()

	s.SetPreview(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2})
	if err := s.Confirm(); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	cur = base.Add(2 * TurnLimit)
	for i := 0; i < 10 && s.Phase() != ui.PhaseFinished; i++ {
		s.Tick(0.016)
	}
	if s.Phase() != ui.PhaseFinished {
		t.Fatal("相手が黙っているのに対戦が終わらない")
	}
	if !s.PeerLeft() {
		t.Errorf("応答の途絶が勝ちになっていない（reason=%q）", s.AbortReason())
	}
	if s.AbortReason() == "" {
		t.Error("なぜ終わったのかが残っていない")
	}
}

// Start を呼び忘れても制限時間は働く。最初に期限を参照した時点で数え始める。
// PlayState は対戦画面が生きている間、毎フレーム参照する。
func TestDeadlineAnchorsOnFirstUseWhenStartForgotten(t *testing.T) {
	ta, _ := NewPipe()
	base := time.Unix(1_700_000_000, 0)
	cur := base
	a := NewNetOpponent(ta, func() time.Time { return cur })
	defer a.Close()
	// Start() は呼ばない

	cur = base.Add(7 * time.Second)
	got, ok := a.Deadline(0)
	if !ok {
		t.Fatal("Start を呼ばないと期限が立たない。呼び忘れで制限時間が消える")
	}
	if want := cur.Add(TurnLimit); !got.Equal(want) {
		t.Errorf("期限 = %v, 期待 %v（最初に参照した時刻から30秒）", got, want)
	}
	// 2回目以降は動かない。
	cur = base.Add(15 * time.Second)
	if got2, _ := a.Deadline(0); !got2.Equal(got) {
		t.Errorf("参照するたびに期限が %v から %v へ動いた", got, got2)
	}
}

// 一度打ち切ったら、どの経路も同じエラーを返す。対戦は終わっているので、
// 別の経路から続きが動き出してはならない。
func TestExpiredWaitPoisonsEveryPath(t *testing.T) {
	ta, tb := NewPipe()
	base := time.Unix(1_700_000_000, 0)
	cur := base
	a := NewNetOpponent(ta, func() time.Time { return cur })
	defer a.Close()
	defer tb.Close()
	a.Start()
	if err := a.Submit(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2}, 0); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	a.Poll(0) // 待ち始め

	cur = base.Add(TurnLimit + time.Second)
	if _, _, err := a.Poll(0); err == nil {
		t.Fatal("打ち切られなかった")
	}
	if err := a.Submit(rules.Circle{CX: 0.3, CY: 0.3, R: 0.2}, 1); err == nil {
		t.Error("打ち切ったあと Submit が通った")
	}
	if _, err := a.ConfirmBoard(rules.NewBoard(rules.DefaultConfig()), 0); err == nil {
		t.Error("打ち切ったあと ConfirmBoard が通った")
	}
}

// 再戦が成立したら、制限時間の起点は新しい対戦の開始時刻になる（設計 §4）。
//
// **これは黙って壊れる種類の欠陥である。** 起点を捨て忘れると、対戦2のターン0の
// 期限が対戦1の開始時刻＋30秒のままになり、対戦2は始まった瞬間に時間切れになる。
// 対戦1が終わるまでは何も起きないので、実際に再戦するまで誰も気づかない。
func TestRematchRestartsTheClock(t *testing.T) {
	ta, tb := NewPipe()
	clock := time.Unix(1_700_000_000, 0)
	now := func() time.Time { return clock }
	a := NewNetOpponent(ta, now)
	b := NewNetOpponent(tb, now)
	defer a.Close()
	defer b.Close()

	a.Start()
	b.Start()
	matchOne := clock

	// 対戦1を1ターンぶん進め、時間を進める。
	clock = clock.Add(20 * time.Second)
	if err := a.RequestRematch(0); err != nil {
		t.Fatalf("a.RequestRematch: %v", err)
	}
	if err := b.RequestRematch(0); err != nil {
		t.Fatalf("b.RequestRematch: %v", err)
	}
	clock = clock.Add(5 * time.Second)
	rematchAt := clock

	agree(t, a, b)

	got, ok := a.Deadline(0)
	if !ok {
		t.Fatal("再戦後にターン0の期限が立っていない")
	}
	if want := rematchAt.Add(TurnLimit); !got.Equal(want) {
		t.Errorf("ターン0の期限 = %v, 期待 %v（対戦1の起点 %v を捨てていない）",
			got, want, matchOne)
	}
}

// agree は両者が申し出た再戦の成立を待つ。
func agree(t *testing.T, a, b *NetOpponent) {
	t.Helper()
	for i := 0; i < 50; i++ {
		oka, err := a.PollRematch()
		if err != nil {
			t.Fatalf("a.PollRematch: %v", err)
		}
		okb, err := b.PollRematch()
		if err != nil {
			t.Fatalf("b.PollRematch: %v", err)
		}
		if oka && okb {
			return
		}
	}
	t.Fatal("再戦が成立しなかった")
}

// 相手が返事をしないまま制限時間を過ぎたら、切断として打ち切る（設計 §6）。
func TestRematchGivesUpWhenPeerNeverAnswers(t *testing.T) {
	ta, _ := NewPipe()
	clock := time.Unix(1_700_000_000, 0)
	a := NewNetOpponent(ta, func() time.Time { return clock })
	defer a.Close()

	if err := a.RequestRematch(0); err != nil {
		t.Fatalf("a.RequestRematch: %v", err)
	}

	// 期限の直前では諦めない。
	clock = clock.Add(RematchLimit - time.Second)
	if ok, err := a.PollRematch(); err != nil || ok {
		t.Fatalf("期限前に諦めた: ok=%v err=%v", ok, err)
	}
	if d, ok := a.RematchRemaining(); !ok || d != time.Second {
		t.Errorf("のこり = %v (ok=%v), 期待 1s", d, ok)
	}

	// 過ぎたら切断として扱う。
	clock = clock.Add(2 * time.Second)
	_, err := a.PollRematch()
	if err == nil {
		t.Fatal("期限を過ぎても諦めなかった")
	}
	if !errors.Is(err, ui.ErrPeerGone) {
		t.Errorf("エラー = %v, 期待 ui.ErrPeerGone を含む", err)
	}
	var pf ui.PeerFault
	if !errors.As(err, &pf) || pf.PeerFaultReason() == "" {
		t.Errorf("理由が付いていない: %v", err)
	}
}

// 申し出ていなければ、のこり時間は無い。
func TestRematchRemainingIsAbsentBeforeRequesting(t *testing.T) {
	ta, _ := NewPipe()
	a := NewNetOpponent(ta, func() time.Time { return time.Unix(1_700_000_000, 0) })
	defer a.Close()

	if d, ok := a.RematchRemaining(); ok {
		t.Errorf("申し出る前に のこり %v が出た", d)
	}
	if ok, err := a.PollRematch(); ok || err != nil {
		t.Errorf("申し出る前の PollRematch = %v, %v", ok, err)
	}
}

// 期限を過ぎても、のこり時間は負にならない。
//
// 画面はこの値をそのまま「のこり N秒」と出すので、負を返すと
// 「のこり -3秒」が見える。打ち切りは PollRematch が行うが、
// 描画と打ち切りは同じフレームとは限らない。
func TestRematchRemainingNeverGoesNegative(t *testing.T) {
	ta, _ := NewPipe()
	clock := time.Unix(1_700_000_000, 0)
	a := NewNetOpponent(ta, func() time.Time { return clock })
	defer a.Close()

	if err := a.RequestRematch(0); err != nil {
		t.Fatalf("a.RequestRematch: %v", err)
	}
	clock = clock.Add(RematchLimit + 3*time.Second)
	d, ok := a.RematchRemaining()
	if !ok {
		t.Fatal("のこり時間が消えた")
	}
	if d != 0 {
		t.Errorf("のこり = %v, 期待 0", d)
	}
}
