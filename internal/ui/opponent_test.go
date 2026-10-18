package ui

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"github.com/morststs/inkmaru-web/internal/ai"
	"github.com/morststs/inkmaru-web/internal/rules"
)

// CPU の相手は Poll が即座に ok=true を返す。既存の見た目の挙動を変えないため。
func TestCPUOpponentReturnsImmediately(t *testing.T) {
	o := NewCPUOpponent(ai.Normal, 1)
	defer o.Close()
	cfg := rules.DefaultConfig()
	if sc, ok := o.(interface {
		SetContext(rules.Config, []rules.Move, rules.Player)
	}); ok {
		sc.SetContext(cfg, nil, rules.P2)
	} else {
		// 型アサーションが外れると SetContext が黙って呼ばれなくなり、
		// cfg 未設定のまま半径0が返る。署名を変えたときに気づけるよう明示的に落とす。
		t.Fatal("cpuOpponent が SetContext(cfg, hist, seat) を実装していない")
	}
	if err := o.Submit(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}, 0); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	c, ok, err := o.Poll(0)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if !ok {
		t.Fatal("CPU なのに手が揃っていない")
	}
	if c.R < cfg.MinRadius()-1e-9 || c.R > cfg.MaxRadius()+1e-9 {
		t.Errorf("CPU の半径がレンジ外: %v", c.R)
	}
}

// 相手を差し替えられることの確認。オンライン実装を挿す土台になる。
type stubOpponent struct {
	reply     rules.Circle
	ready     bool
	err       error
	submitErr error
	got       []rules.Circle
	closed    bool
}

func (s *stubOpponent) Submit(c rules.Circle, turn int) error {
	s.got = append(s.got, c)
	return s.submitErr
}
func (s *stubOpponent) Poll(turn int) (rules.Circle, bool, error) {
	if s.err != nil {
		return rules.Circle{}, false, s.err
	}
	return s.reply, s.ready, nil
}
func (s *stubOpponent) Close() error { s.closed = true; return nil }

func TestPlayStateAcceptsInjectedOpponent(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	stub := &stubOpponent{reply: rules.Circle{CX: 0.8, CY: 0.8, R: 0.1}, ready: true}
	s := NewPlayStateWith(cfg, stub)

	s.SetPreview(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2})
	if err := s.Confirm(); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	s.Tick(0.001)
	if len(stub.got) != 1 {
		t.Fatalf("相手に渡された手の数 = %d, 期待 1", len(stub.got))
	}
	if s.Match().Turn() != 1 {
		t.Errorf("ターン = %d, 期待 1", s.Match().Turn())
	}
}

// 相手がまだ返さないときは待ちフェーズに入り、ゲームループを止めない。
func TestPlayStateWaitsForPeer(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	stub := &stubOpponent{ready: false}
	s := NewPlayStateWith(cfg, stub)

	s.SetPreview(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2})
	if err := s.Confirm(); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if s.Phase() != PhaseWaitingPeer {
		t.Fatalf("フェーズ = %v, 期待 PhaseWaitingPeer", s.Phase())
	}
	s.Tick(0.016)
	if s.Phase() != PhaseWaitingPeer {
		t.Errorf("相手が未応答なのにフェーズが進んだ: %v", s.Phase())
	}
	if s.Match().Turn() != 0 {
		t.Errorf("相手が未応答なのにターンが進んだ: %d", s.Match().Turn())
	}

	stub.ready = true
	stub.reply = rules.Circle{CX: 0.8, CY: 0.8, R: 0.1}
	s.Tick(0.016)
	if s.Match().Turn() != 1 {
		t.Errorf("相手の手が届いたのにターンが進まない: %d", s.Match().Turn())
	}
	if s.Phase() != PhaseRevealing {
		t.Errorf("フェーズ = %v, 期待 PhaseRevealing", s.Phase())
	}
}

// 待ちフェーズでは自分の円を描き直せない。確定済みの手だからである。
func TestSetPreviewIgnoredWhileWaitingPeer(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	stub := &stubOpponent{ready: false}
	s := NewPlayStateWith(cfg, stub)

	s.SetPreview(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2})
	if err := s.Confirm(); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	s.SetPreview(rules.Circle{CX: 0.9, CY: 0.9, R: 0.1})
	if _, ok := s.Preview(); ok {
		t.Error("待ちフェーズで SetPreview が通ってしまった")
	}
	if err := s.Confirm(); !errors.Is(err, ErrNotDrawing) {
		t.Errorf("エラー = %v, 期待 ErrNotDrawing", err)
	}

	stub.ready = true
	stub.reply = rules.Circle{CX: 0.8, CY: 0.8, R: 0.1}
	s.Tick(0.016)
	// 確定した円がそのまま盤面に載っていること（描き直しに差し替わっていない）。
	if mv := s.LastMoves()[0]; mv.CX != 0.2 || mv.CY != 0.2 {
		t.Errorf("確定した自分の円が差し替わっている: %+v", mv)
	}
}

// 切断は切断した側の負け（仕様書 §5.4）。中断とは扱いを分ける。
func TestPeerGoneIsAWin(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	// 番兵をそのまま返すのではなく、理由の空の PeerFault を返す。オンラインの
	// 実装（netplay.NetOpponent）が実際に返すのはこの形である。素の番兵だと
	// finishOnOpponentError の errors.As 分岐に一度も入らないため、下の
	// AbortReason の検査が原理的に落ちないテストになる（変異 U7 で実証済み）。
	// 番兵をそのまま／包んで返す形は TestPlainPeerGoneHasNoReason が見ている。
	stub := &stubOpponent{err: peerFaultErr{reason: "", cause: ErrPeerGone}}
	s := NewPlayStateWith(cfg, stub)

	s.SetPreview(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2})
	s.Confirm()
	s.Tick(0.016)

	if s.Phase() != PhaseFinished {
		t.Fatalf("フェーズ = %v, 期待 PhaseFinished", s.Phase())
	}
	if !s.PeerLeft() {
		t.Error("相手の切断が勝ちとして扱われていない")
	}
	if s.AbortReason() != "" {
		t.Errorf("切断は中断ではないので理由が入るべきでない: %q", s.AbortReason())
	}
}

// 不正検出などの中断は勝敗をつけず、理由だけを残す。
func TestPollErrorAbortsWithoutWin(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	stub := &stubOpponent{err: errors.New("netplay: 中断 (board-mismatch)")}
	s := NewPlayStateWith(cfg, stub)

	s.SetPreview(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2})
	s.Confirm()
	s.Tick(0.016)

	if s.Phase() != PhaseFinished {
		t.Fatalf("フェーズ = %v, 期待 PhaseFinished", s.Phase())
	}
	if s.PeerLeft() {
		t.Error("中断が切断勝ちとして扱われている")
	}
	if s.AbortReason() == "" {
		t.Error("中断の理由が残っていない")
	}
}

// 盤面ハッシュの照合を持つ相手では、合意できるまで次のターンに進まない。
type boardStub struct {
	stubOpponent
	agreed   bool
	boardErr error
	calls    int
}

func (b *boardStub) ConfirmBoard(bd *rules.Board, turn int) (bool, error) {
	b.calls++
	return b.agreed, b.boardErr
}

func TestBoardConfirmGatesNextTurn(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	stub := &boardStub{}
	stub.ready = true
	stub.reply = rules.Circle{CX: 0.8, CY: 0.8, R: 0.1}
	s := NewPlayStateWith(cfg, stub)

	s.SetPreview(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2})
	if err := s.Confirm(); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	s.Tick(RevealSeconds)
	s.Tick(RevealSeconds)
	if s.Phase() != PhaseRevealing {
		t.Fatalf("盤面ハッシュ未合意なのにフェーズが進んだ: %v", s.Phase())
	}
	if stub.calls == 0 {
		t.Fatal("ConfirmBoard が呼ばれていない")
	}

	stub.agreed = true
	s.Tick(RevealSeconds)
	if s.Phase() != PhaseDrawing {
		t.Errorf("合意後もフェーズが進まない: %v", s.Phase())
	}
}

func TestBoardConfirmErrorAborts(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	stub := &boardStub{boardErr: errors.New("netplay: 中断 (board-mismatch)")}
	stub.ready = true
	stub.reply = rules.Circle{CX: 0.8, CY: 0.8, R: 0.1}
	s := NewPlayStateWith(cfg, stub)

	s.SetPreview(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2})
	s.Confirm()
	s.Tick(RevealSeconds)
	s.Tick(RevealSeconds)
	if s.Phase() != PhaseFinished {
		t.Fatalf("フェーズ = %v, 期待 PhaseFinished", s.Phase())
	}
	if s.AbortReason() == "" {
		t.Error("中断の理由が残っていない")
	}
}

// CPU の手は「そのターンにプレイヤーが確定した円」に依存してはならない。
// Opponent.Submit は自分の円を引数に取るが、これは確定後にのみ呼ばれ、
// かつ CPU 実装はその値を読まない。読んでしまうと同時進行の前提が壊れる。
//
// **全ターンを見ること。** ターン0の1手だけを比べると、「ターン1以降だけ盗み見る」
// 実装（プレイすれば一目で分かる露骨な後出し）が素通りする。実際にレビューの
// 変異 M-A3 がそれで、旧版のこのテストは 39件すべて PASS させてしまった。
//
// **比べ方に注意。** プレイヤーの円を全ターンまるごと変えて手の列を比較しては
// いけない。CPU は「過去に確定した手の履歴」を見て指すのが正しい振る舞いなので、
// 過去の手が違えば以降の手が変わるのは当然であり、そのテストは正しい実装を
// 落としてしまう（実際に落ちることを確認した）。守るべき性質は
// 「同じ履歴のもとで、そのターンの自分の円が何であっても CPU の手は同じ」である。
// そこで、ターン k **だけ**プレイヤーの円が異なる2局を走らせ、ターン k の
// CPU の手を比べる。これを全ターンについて繰り返す。
func TestCPUChoiceIndependentOfPlayerCircle(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64

	// alt が真のターンだけ、プレイヤーは別の円を描く。
	playUntil := func(k int, alt bool) rules.Circle {
		s := NewPlayStateWith(cfg, NewCPUOpponent(ai.Normal, 42))
		var cpuAtK rules.Circle
		for turn := 0; turn <= k; turn++ {
			c := rules.Circle{CX: 0.15 + 0.05*float64(turn), CY: 0.2, R: 0.22}
			if alt && turn == k {
				c = rules.Circle{CX: 0.85, CY: 0.9, R: 0.05}
			}
			s.SetPreview(c)
			if err := s.Confirm(); err != nil {
				t.Fatalf("ターン %d の Confirm: %v", turn, err)
			}
			if turn == k {
				cpuAtK = s.LastMoves()[1] // LastMoves は [P1, P2] の順
			}
			s.Tick(RevealSeconds)
		}
		return cpuAtK
	}

	for k := 0; k < cfg.Turns; k++ {
		a := playUntil(k, false)
		b := playUntil(k, true)
		if a != b {
			t.Errorf("ターン %d: そのターンのプレイヤーの円で CPU の手が変わった: %+v vs %+v",
				k, a, b)
		}
	}
}

// Close は相手に伝わること。オンライン実装では接続の後始末に使う。
func TestPlayStateCloseClosesOpponent(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	stub := &stubOpponent{}
	s := NewPlayStateWith(cfg, stub)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !stub.closed {
		t.Error("相手が閉じられていない")
	}
}

// Submit の時点で相手が消えていた場合も、切断は切断した側の負けである。
//
// これはオンラインで最も起きやすい切断のかたちである。自分が描いている間に
// 相手がタブを閉じ、そのあと自分が確定を押すと、送信そのものが失敗する
// （netplay.Session.Submit → Transport.Send の経路）。Poll 側だけを分類していると、
// ここで game が描画フェーズのまま永久に固まり、仕様書 §5.4 が発動しない。
func TestPeerGoneAtSubmitIsAWin(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	// 理由の空の PeerFault で渡す理由は TestPeerGoneIsAWin のコメント参照。
	stub := &stubOpponent{submitErr: peerFaultErr{reason: "", cause: ErrPeerGone}}
	s := NewPlayStateWith(cfg, stub)

	s.SetPreview(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2})
	if err := s.Confirm(); err != nil {
		t.Fatalf("切断は対戦の正常な終了なので Confirm はエラーを返すべきでない: %v", err)
	}
	if s.Phase() != PhaseFinished {
		t.Fatalf("フェーズ = %v, 期待 PhaseFinished", s.Phase())
	}
	if !s.PeerLeft() {
		t.Error("Submit 時点の切断が勝ちとして扱われていない")
	}
	if s.AbortReason() != "" {
		t.Errorf("切断は中断ではないので理由が入るべきでない: %q", s.AbortReason())
	}
	// 何フレーム回しても状態が変わらないこと（固まっていないことの確認）。
	for i := 0; i < 100; i++ {
		s.Tick(0.016)
	}
	if s.Phase() != PhaseFinished || !s.PeerLeft() {
		t.Errorf("フェーズが揺れた: phase=%v peerLeft=%v", s.Phase(), s.PeerLeft())
	}
}

// Submit のその他のエラーは中断として扱う。勝敗はつけない。
func TestSubmitErrorAbortsWithoutWin(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	stub := &stubOpponent{submitErr: errors.New("netplay: 中断 (protocol)")}
	s := NewPlayStateWith(cfg, stub)

	s.SetPreview(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2})
	if err := s.Confirm(); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if s.Phase() != PhaseFinished {
		t.Fatalf("フェーズ = %v, 期待 PhaseFinished", s.Phase())
	}
	if s.PeerLeft() {
		t.Error("中断が切断勝ちとして扱われている")
	}
	if s.AbortReason() == "" {
		t.Error("中断の理由が残っていない")
	}
}

// mutatingBoardStub は渡された盤面を書き換える不注意な相手。
type mutatingBoardStub struct {
	stubOpponent
}

func (b *mutatingBoardStub) ConfirmBoard(bd *rules.Board, turn int) (bool, error) {
	// ハッシュを取るだけのはずの実装が、うっかり盤面を汚した場合を模す。
	bd.Apply(rules.Circle{CX: 0.5, CY: 0.5, R: 0.45}, rules.P2, 100)
	return true, nil
}

// ConfirmBoard には複製を渡す。相手の実装に本物の盤面を握らせない。
func TestConfirmBoardReceivesACopy(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	stub := &mutatingBoardStub{}
	stub.ready = true
	stub.reply = rules.Circle{CX: 0.8, CY: 0.8, R: 0.1}
	s := NewPlayStateWith(cfg, stub)

	s.SetPreview(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2})
	if err := s.Confirm(); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	before := s.Match().Board().Score()
	s.Tick(RevealSeconds)
	after := s.Match().Board().Score()
	if before != after {
		t.Errorf("相手が本物の盤面を書き換えた: %+v → %+v", before, after)
	}
}

// ターンの解決に失敗したら、黙って待ちフェーズに固まらず中断として見せる。
func TestResolveFailureAborts(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	stub := &stubOpponent{ready: true, reply: rules.Circle{CX: 0.8, CY: 0.8, R: 0.1}}
	s := NewPlayStateWith(cfg, stub)

	// 内部の Match に横から P1 の手を入れておくと、解決時の
	// match.Submit(P1) が ErrAlreadySubmitted で失敗する。
	if err := s.Match().Submit(rules.P1, rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}); err != nil {
		t.Fatalf("下準備の Submit: %v", err)
	}

	s.SetPreview(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2})
	s.Confirm()
	for i := 0; i < 10; i++ {
		s.Tick(0.016)
	}
	if s.Phase() != PhaseFinished {
		t.Fatalf("解決に失敗したのに固まっている: phase=%v", s.Phase())
	}
	if s.AbortReason() == "" {
		t.Error("中断の理由が残っていない")
	}
	if s.PeerLeft() {
		t.Error("自分側の解決失敗が切断勝ちになっている")
	}
}

// 「相手が消えた」という同じ事象は、どの経路で分かっても同じ結末でなければならない。
//
// 経路は3つある。自分の手を送るとき（Submit）、相手の手を待つとき（Poll）、
// 盤面ハッシュを突き合わせるとき（ConfirmBoard）である。netplay.Session は
// このどれからでも ErrPeerGone を返しうる（送信の tr.Send 経由と、
// drain が s.aborted に積む経路の両方がある）。分類がひとつでも抜けていると、
// 相手が消えた瞬間が1フレーム違うだけで、画面が「あなたの勝ち」と
// 「エラーで中断」の間を行き来することになる。
func TestPeerGoneIsAWinOnEveryPath(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	// 実装は errors.Is で判定すること。包まれていても拾えなければならない。
	// 形はオンラインの実装が実際に返すもの（理由の空の PeerFault）に合わせる。
	// 素の番兵だと下の AbortReason の検査が原理的に落ちないテストになる。
	wrapped := peerFaultErr{reason: "", cause: fmt.Errorf("経路が切れた: %w", ErrPeerGone)}
	reply := rules.Circle{CX: 0.8, CY: 0.8, R: 0.1}

	cases := []struct {
		name string
		run  func(t *testing.T) *PlayState
	}{
		{"Submit", func(t *testing.T) *PlayState {
			s := NewPlayStateWith(cfg, &stubOpponent{submitErr: wrapped})
			s.SetPreview(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2})
			if err := s.Confirm(); err != nil {
				t.Fatalf("Confirm: %v", err)
			}
			return s
		}},
		{"Poll", func(t *testing.T) *PlayState {
			s := NewPlayStateWith(cfg, &stubOpponent{err: wrapped})
			s.SetPreview(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2})
			if err := s.Confirm(); err != nil {
				t.Fatalf("Confirm: %v", err)
			}
			s.Tick(0.016)
			return s
		}},
		{"ConfirmBoard", func(t *testing.T) *PlayState {
			stub := &boardStub{boardErr: wrapped}
			stub.ready = true
			stub.reply = reply
			s := NewPlayStateWith(cfg, stub)
			s.SetPreview(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2})
			if err := s.Confirm(); err != nil {
				t.Fatalf("Confirm: %v", err)
			}
			s.Tick(RevealSeconds)
			if stub.calls == 0 {
				t.Fatal("下準備が効いていない: ConfirmBoard が呼ばれていない")
			}
			return s
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := c.run(t)
			if s.Phase() != PhaseFinished {
				t.Errorf("フェーズ = %v, 期待 PhaseFinished", s.Phase())
			}
			if !s.PeerLeft() {
				t.Errorf("%s 経路の切断が勝ちとして扱われていない", c.name)
			}
			if s.AbortReason() != "" {
				t.Errorf("切断は中断ではないので理由が入るべきでない: %q", s.AbortReason())
			}
		})
	}
}

// recordingBoardStub は渡された盤面の同一性を記録する。
type recordingBoardStub struct {
	stubOpponent
	agreed bool
	boards []*rules.Board
}

func (b *recordingBoardStub) ConfirmBoard(bd *rules.Board, turn int) (bool, error) {
	b.boards = append(b.boards, bd)
	return b.agreed, nil
}

// 相手のハッシュを待っている間、複製は1回だけ作って使い回す。
//
// 待っている間、盤面は変化しない（次のターンはまだ始まっていない）。
// にもかかわらず毎フレーム Clone すると、GridN=256 で 320KB の複製が
// 毎フレーム走り、WASM では無視できない負担になる。
func TestConfirmBoardCloneIsHoisted(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	stub := &recordingBoardStub{}
	stub.ready = true
	stub.reply = rules.Circle{CX: 0.8, CY: 0.8, R: 0.1}
	s := NewPlayStateWith(cfg, stub)

	s.SetPreview(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2})
	if err := s.Confirm(); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	// 未合意のまま3フレーム待たせる。
	for i := 0; i < 3; i++ {
		s.Tick(RevealSeconds)
	}
	if len(stub.boards) < 3 {
		t.Fatalf("ConfirmBoard の呼び出し回数 = %d, 期待 3以上", len(stub.boards))
	}
	for i, b := range stub.boards {
		if b != stub.boards[0] {
			t.Errorf("待機中の %d 回目で複製が作り直された", i)
		}
	}
	if stub.boards[0] == s.Match().Board() {
		t.Error("本物の盤面がそのまま渡っている")
	}

	// 合意したら次のターンへ進み、複製は破棄されること。
	// 使い回したままだと、次のターンで古い盤面のハッシュを突き合わせてしまう。
	first := stub.boards[0]
	stub.agreed = true
	s.Tick(RevealSeconds)
	if s.Phase() != PhaseDrawing {
		t.Fatalf("合意後のフェーズ = %v, 期待 PhaseDrawing", s.Phase())
	}
	stub.agreed = false
	s.SetPreview(rules.Circle{CX: 0.3, CY: 0.3, R: 0.2})
	if err := s.Confirm(); err != nil {
		t.Fatalf("2ターン目の Confirm: %v", err)
	}
	s.Tick(RevealSeconds)
	last := stub.boards[len(stub.boards)-1]
	if last == first {
		t.Error("次のターンで前のターンの複製が使い回されている")
	}
	if last.Score() == first.Score() {
		t.Error("次のターンの複製が更新されていない（同じ盤面のまま）")
	}
}

// TestCPUSeatComesFromPlayState は CPU が自分の席を PlayState から受け取ることを固定する。
//
// 以前は cpuOpponent が rules.P2 を決め打ちしていた。CPU 対戦では席が常に
// 「プレイヤー=P1・CPU=P2」なので実害は無かったが、決め打ちは PlayState 側の
// 席と食い違っても黙って通る。オンライン対戦では同じ形の思い込み
// （両者が自分を P1 とする）が board-mismatch を引き起こしていたので、
// CPU 側にも決め打ちを残さない。
func TestCPUSeatComesFromPlayState(t *testing.T) {
	spy := &seatSpy{}
	opp := newCPUOpponentWith(spy, 1)

	sc, ok := opp.(interface {
		SetContext(rules.Config, []rules.Move, rules.Player)
	})
	if !ok {
		t.Fatal("cpuOpponent が SetContext(cfg, hist, seat) を実装していない")
	}
	sc.SetContext(rules.DefaultConfig(), nil, rules.P1)
	if err := opp.Submit(rules.Circle{}, 0); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if spy.seat != rules.P1 {
		t.Errorf("CPU が使った席 = %v, 渡した席 = %v", spy.seat, rules.P1)
	}
}

// seatSpy は Choose に渡された席を記録するだけの Chooser。
type seatSpy struct{ seat rules.Player }

func (s *seatSpy) Choose(_ []rules.Move, _ rules.Config, p rules.Player, _ int, _ *rand.Rand) rules.Circle {
	s.seat = p
	return rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}
}
