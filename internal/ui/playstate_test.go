package ui

import (
	"errors"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/morststs/inkmaru-web/internal/ai"
	"github.com/morststs/inkmaru-web/internal/rules"
)

func newTestState() *PlayState {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	return NewPlayState(cfg, ai.Normal, 1)
}

// spyChooser は渡された履歴を記録するだけの Chooser。
// CPU がプレイヤーの未確定の円を覗いていないことを検証するために使う。
type spyChooser struct {
	seen  [][]rules.Move
	reply rules.Circle
}

func (s *spyChooser) Choose(hist []rules.Move, cfg rules.Config, me rules.Player, turn int, rnd *rand.Rand) rules.Circle {
	s.seen = append(s.seen, append([]rules.Move(nil), hist...))
	return s.reply
}

func TestPlayStateStartsInDrawingPhase(t *testing.T) {
	s := newTestState()
	if s.Phase() != PhaseDrawing {
		t.Errorf("初期フェーズ = %v, 期待 PhaseDrawing", s.Phase())
	}
	if _, ok := s.Preview(); ok {
		t.Error("初期状態でプレビューが存在している")
	}
}

func TestConfirmRequiresPreview(t *testing.T) {
	s := newTestState()
	if err := s.Confirm(); !errors.Is(err, ErrNoPreview) {
		t.Errorf("エラー = %v, 期待 ErrNoPreview", err)
	}
}

func TestConfirmEntersRevealAndAdvancesTurn(t *testing.T) {
	s := newTestState()
	s.SetPreview(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2})
	if err := s.Confirm(); err != nil {
		t.Fatalf("Confirm が失敗した: %v", err)
	}
	if s.Phase() != PhaseRevealing {
		t.Fatalf("フェーズ = %v, 期待 PhaseRevealing", s.Phase())
	}
	if s.Match().Turn() != 1 {
		t.Errorf("ターン = %d, 期待 1", s.Match().Turn())
	}
	// 演出中は盤面の「前の状態」が残っていること。
	if s.BoardBefore().Score().Cells[rules.P1] != 0 {
		t.Error("BoardBefore が確定前の盤面になっていない")
	}
}

func TestTickCompletesRevealAndReturnsToDrawing(t *testing.T) {
	s := newTestState()
	s.SetPreview(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2})
	s.Confirm()
	s.Tick(RevealSeconds / 2)
	if s.Phase() != PhaseRevealing {
		t.Fatal("演出の途中でフェーズが変わった")
	}
	if p := s.RevealProgress(); p <= 0 || p >= 1 {
		t.Errorf("演出の進捗 = %v, 期待 0 < p < 1", p)
	}
	s.Tick(RevealSeconds)
	if s.Phase() != PhaseDrawing {
		t.Errorf("フェーズ = %v, 期待 PhaseDrawing", s.Phase())
	}
	if _, ok := s.Preview(); ok {
		t.Error("次のターンにプレビューが持ち越されている")
	}
}

func TestPlayThroughAllTurnsFinishes(t *testing.T) {
	s := newTestState()
	for i := 0; i < s.Match().Config().Turns; i++ {
		s.SetPreview(rules.Circle{CX: 0.3 + 0.1*float64(i), CY: 0.5, R: 0.18})
		if err := s.Confirm(); err != nil {
			t.Fatalf("ターン %d の Confirm が失敗した: %v", i, err)
		}
		s.Tick(RevealSeconds)
	}
	if s.Phase() != PhaseFinished {
		t.Fatalf("フェーズ = %v, 期待 PhaseFinished", s.Phase())
	}
	if s.Match().Result() == rules.InProgress {
		t.Error("終了しているのに結果が InProgress")
	}
}

func TestPreviewScoreDoesNotMutateMatch(t *testing.T) {
	s := newTestState()
	before := s.Match().Board().Score()
	s.SetPreview(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2})
	ps := s.PreviewScore()
	if ps.Cells[rules.P1] == 0 {
		t.Error("プレビューの陣地が計上されていない")
	}
	if s.Match().Board().Score() != before {
		t.Error("PreviewScore が本物の盤面を書き換えた")
	}
}

func TestConfirmRejectedDuringReveal(t *testing.T) {
	s := newTestState()
	s.SetPreview(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2})
	s.Confirm()
	s.SetPreview(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2})
	if err := s.Confirm(); !errors.Is(err, ErrNotDrawing) {
		t.Errorf("エラー = %v, 期待 ErrNotDrawing", err)
	}
}

func TestSetPreviewIgnoredDuringReveal(t *testing.T) {
	s := newTestState()
	s.SetPreview(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2})
	s.Confirm()
	// 演出中の SetPreview は無視される
	s.SetPreview(rules.Circle{CX: 0.2, CY: 0.2, R: 0.2})
	s.Tick(RevealSeconds)
	if _, ok := s.Preview(); ok {
		t.Error("演出中の SetPreview が有効になった")
	}
}

func TestCPUNeverSeesUnconfirmedCircle(t *testing.T) {
	// プレイヤーの未確定の円が CPU に渡されていないことを検証する。
	// このテストは Chooser.Choose の引数を監視する spy 実装を使い、
	// CPU が受け取る履歴スライスの内容を検査する。
	//
	// **何を検査するか:** CPU に渡された履歴に、現在のターンのプレイヤーの円が
	// 含まれていないこと。これは Preview や Player1の手の情報が
	// Chooser.Choose の引数に漏れていないことを検証する。
	//
	// **何を検査しないか:** Confirm 内での Submit と CPU.Choose の順序。
	// なぜなら Match.History() は解決済みの手だけを返すため、
	// Submit の順序を変えても (Resolve 前の限り) History() は
	// 未確定の手を含まないから。真の保証は internal/rules の
	// TestHistoryExcludesUnresolvedMoves が担当する。
	//
	// 各ターンで異なる中心座標を使うことで、現在のターンの円が
	// 履歴に含まれていないことを確認できる。
	const basePlayerCX = 0.123
	const basePlayerCY = 0.456
	const tolerance = 0.01
	const turnCount = 3

	spy := &spyChooser{
		reply: rules.Circle{CX: 0.7, CY: 0.7, R: 0.15},
	}
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	// テストパッケージ内なので、直接 opp フィールドを CPU 相手の
	// Chooser 差し替え版に置き換える。
	s := NewPlayStateWith(cfg, newCPUOpponentWith(spy, 1))

	for turn := 0; turn < turnCount; turn++ {
		// 各ターンで異なる座標を使用（現在のターンの円を区別するため）
		currentCX := basePlayerCX + float64(turn)*0.05
		currentCY := basePlayerCY
		playerCircle := rules.Circle{CX: currentCX, CY: currentCY, R: 0.2}
		s.SetPreview(playerCircle)
		if err := s.Confirm(); err != nil {
			t.Fatalf("ターン %d の Confirm が失敗した: %v", turn, err)
		}
		s.Tick(RevealSeconds)

		// このターンで CPU が受け取った履歴を検証
		if turn >= len(spy.seen) {
			t.Fatalf("ターン %d の履歴が記録されていない", turn)
		}
		hist := spy.seen[turn]

		// ターン N では、過去に確定した両プレイヤーの手が2*N個ある
		expectedLen := 2 * turn
		if len(hist) != expectedLen {
			t.Errorf("ターン %d の履歴の長さ = %d, 期待 %d", turn, len(hist), expectedLen)
		}

		// 現在のターンの中心座標が履歴に含まれていないこと
		// （過去のターンの座標は含まれることもあるが、現在のターンは含まれない）
		for i, move := range hist {
			if math.Abs(move.Circle.CX-currentCX) < tolerance && math.Abs(move.Circle.CY-currentCY) < tolerance {
				t.Errorf("ターン %d の履歴[%d] に現在のターンの円が含まれている: %+v", turn, i, move.Circle)
			}
		}
	}
}

// 席は既定で P1。第1段階の CPU 対戦の挙動を変えないための既定である。
func TestSeatDefaultsToP1(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	if got := newTestState().Seat(); got != rules.P1 {
		t.Errorf("NewPlayState の席 = %v, 期待 P1", got)
	}
	if got := NewPlayStateWith(cfg, &stubOpponent{}).Seat(); got != rules.P1 {
		t.Errorf("NewPlayStateWith の席 = %v, 期待 P1", got)
	}
	// P1・P2 以外は P1 に丸める。
	if got := NewPlayStateWithSeat(cfg, &stubOpponent{}, rules.Neutral).Seat(); got != rules.P1 {
		t.Errorf("不正な席 = %v, 期待 P1", got)
	}
}

// 席が P2 なら、自分の円は P2 として盤面に入り、相手の円が P1 になる。
// ここが自分の席を見ずに P1 固定だと、オンライン対戦の2人の盤面が鏡像になり、
// 盤面ハッシュの照合が1ターン目で必ず落ちる。
func TestSeatP2AppliesMovesToP2(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	peer := rules.Circle{CX: 0.8, CY: 0.8, R: 0.1}
	stub := &stubOpponent{reply: peer, ready: true}
	s := NewPlayStateWithSeat(cfg, stub, rules.P2)

	mine := rules.Circle{CX: 0.2, CY: 0.2, R: 0.2}
	s.SetPreview(mine)
	// プレビューの得点も自分の席で数える。
	if ps := s.PreviewScore(); ps.Cells[rules.P2] == 0 || ps.Cells[rules.P1] != 0 {
		t.Errorf("プレビューの得点 P1=%d P2=%d, 期待 P1=0・P2>0", ps.Cells[rules.P1], ps.Cells[rules.P2])
	}
	if err := s.Confirm(); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	// 解決後の手は [P1の円, P2の円] の順で返る。席が P2 なので入れ替わる。
	mv := s.LastMoves()
	if mv[0] != cfg.ClampCircle(peer) {
		t.Errorf("P1 の円 = %v, 期待 %v（相手の円）", mv[0], peer)
	}
	if mv[1] != cfg.ClampCircle(mine) {
		t.Errorf("P2 の円 = %v, 期待 %v（自分の円）", mv[1], mine)
	}
	// 盤面でも、自分の円の中心は P2 の陣地になっている。
	n := s.Match().Board().GridN()
	if got := s.Match().Board().OwnerAt(int(mine.CX*float64(n)), int(mine.CY*float64(n))); got != rules.P2 {
		t.Errorf("自分の円の中心の所有者 = %v, 期待 P2", got)
	}
}

// fakeRematcher は再戦できる相手役。Opponent と Rematcher を満たす。
type fakeRematcher struct {
	fixedOpponent
	asked     int
	lastTurn  int
	agreed    bool
	pollErr   error
	remaining time.Duration
	hasRem    bool
}

func (f *fakeRematcher) RequestRematch(turn int) error {
	f.asked++
	f.lastTurn = turn
	return nil
}
func (f *fakeRematcher) PollRematch() (bool, error)              { return f.agreed, f.pollErr }
func (f *fakeRematcher) RematchRemaining() (time.Duration, bool) { return f.remaining, f.hasRem }

// 再戦を出すかどうかは相手役の能力で決まる。CPU の相手役は再戦できない。
func TestCanRematchOnlyWithACapableOpponent(t *testing.T) {
	cfg := smallConfig()

	plain := NewPlayStateWithSeat(cfg, &fixedOpponent{c: rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}}, rules.P1)
	finish(t, plain, cfg)
	if plain.CanRematch() {
		t.Error("再戦できない相手役で CanRematch が真になった")
	}

	rm := &fakeRematcher{fixedOpponent: fixedOpponent{c: rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}}}
	s := NewPlayStateWithSeat(cfg, rm, rules.P1)
	if s.CanRematch() {
		t.Error("対戦の途中で CanRematch が真になった")
	}
	finish(t, s, cfg)
	if !s.CanRematch() {
		t.Error("正常に終わったのに CanRematch が偽のまま")
	}
}

// 中断で終わった対戦では再戦を出さない（設計 §6）。
func TestCanRematchIsFalseAfterAbort(t *testing.T) {
	cfg := smallConfig()
	rm := &fakeRematcher{fixedOpponent: fixedOpponent{c: rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}}}
	s := NewPlayStateWithSeat(cfg, rm, rules.P1)
	s.abortWith(errors.New("相手のデータが不正"))
	if s.Phase() != PhaseFinished {
		t.Fatalf("中断後の phase = %v", s.Phase())
	}
	if s.CanRematch() {
		t.Error("中断で終わった対戦に再戦を出した")
	}
	if err := s.RequestRematch(); err == nil {
		t.Error("中断で終わった対戦で再戦を申し出られた")
	}
	if rm.asked != 0 {
		t.Errorf("相手役に %d 回申し出た。0 であるべき", rm.asked)
	}
}

// 相手が離脱して終わった対戦でも再戦は出さない（相手がもう居ない）。
func TestCanRematchIsFalseAfterPeerLeft(t *testing.T) {
	cfg := smallConfig()
	rm := &fakeRematcher{fixedOpponent: fixedOpponent{c: rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}}}
	s := NewPlayStateWithSeat(cfg, rm, rules.P1)
	s.finishOnOpponentError(ErrPeerGone)
	if !s.PeerLeft() {
		t.Fatal("離脱として扱われていない")
	}
	if s.CanRematch() {
		t.Error("相手が離脱したのに再戦を出した")
	}
}

// 申し出は1回だけ相手役に届き、押したことが画面から分かる。
func TestRequestRematchIsIdempotent(t *testing.T) {
	cfg := smallConfig()
	rm := &fakeRematcher{fixedOpponent: fixedOpponent{c: rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}}}
	s := NewPlayStateWithSeat(cfg, rm, rules.P1)
	finish(t, s, cfg)

	if s.RematchAsked() {
		t.Error("押す前から申し出たことになっている")
	}
	for i := 0; i < 3; i++ {
		if err := s.RequestRematch(); err != nil {
			t.Fatalf("RequestRematch: %v", err)
		}
	}
	if !s.RematchAsked() {
		t.Error("押したのに申し出たことになっていない")
	}
	if rm.asked != 1 {
		t.Errorf("相手役への申し出が %d 回。1回であるべき", rm.asked)
	}
	if want := cfg.Turns - 1; rm.lastTurn != want {
		t.Errorf("申し出に載せたターン = %d, 期待 %d（終わった対戦の最終ターン）", rm.lastTurn, want)
	}
}

// 申し出るまでは相手の返事を見に行かない。
//
// 見に行くと、こちらが結果を読んでいるあいだに相手の60秒が進む。
func TestPollRematchDoesNothingBeforeAsking(t *testing.T) {
	cfg := smallConfig()
	rm := &fakeRematcher{fixedOpponent: fixedOpponent{c: rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}}, agreed: true}
	s := NewPlayStateWithSeat(cfg, rm, rules.P1)
	finish(t, s, cfg)

	ok, err := s.PollRematch()
	if err != nil {
		t.Fatalf("PollRematch: %v", err)
	}
	if ok {
		t.Error("押していないのに再戦が成立した")
	}
	if err := s.RequestRematch(); err != nil {
		t.Fatalf("RequestRematch: %v", err)
	}
	ok, err = s.PollRematch()
	if err != nil || !ok {
		t.Errorf("押したあとの PollRematch = %v, %v", ok, err)
	}
}

// smallConfig は格子を粗くした設定。盤面の計算を軽くするだけで、判定は変わらない。
func smallConfig() rules.Config {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	return cfg
}

// finish は対戦を最後まで進める。
func finish(t *testing.T, s *PlayState, cfg rules.Config) {
	t.Helper()
	for i := 0; i < cfg.Turns; i++ {
		s.SetPreview(rules.Circle{CX: 0.5, CY: 0.5, R: cfg.MinRadius()})
		if err := s.Confirm(); err != nil {
			t.Fatalf("ターン%d の Confirm: %v", i, err)
		}
		for j := 0; j < 200 && s.Phase() == PhaseRevealing; j++ {
			s.Tick(0.1)
		}
	}
	if s.Phase() != PhaseFinished {
		t.Fatalf("対戦が終わっていない: phase = %v", s.Phase())
	}
}

// 相手の返事が得られなかったことは画面に残す。タイトルへ飛ばさない。
//
// **勝敗を上書きしてはならない。** 相手の離脱（PeerLeft）として扱うと
// ResultMessage が「あなたの勝ち」に変えてしまい、負けた対戦が勝ちに見える。
// 対戦そのものは正常に終わっており、後から変わってよい結果ではない。
func TestRematchFailureKeepsTheResult(t *testing.T) {
	cfg := smallConfig()
	rm := &fakeRematcher{fixedOpponent: fixedOpponent{c: rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}}}
	s := NewPlayStateWithSeat(cfg, rm, rules.P1)
	finish(t, s, cfg)
	before := s.Match().Result()

	if err := s.RequestRematch(); err != nil {
		t.Fatalf("RequestRematch: %v", err)
	}
	rm.pollErr = ErrPeerGone
	if _, err := s.PollRematch(); err == nil {
		t.Fatal("相手が消えたのにエラーが返らない")
	}

	if s.PeerLeft() {
		t.Error("再戦の失敗を対戦中の離脱として扱っている（勝敗が上書きされる）")
	}
	if s.Match().Result() != before {
		t.Errorf("勝敗が変わった: %v → %v", before, s.Match().Result())
	}
	if s.CanRematch() {
		t.Error("失敗したあとも再戦を出している")
	}
	if got := s.RematchStatus(); len(got) == 0 {
		t.Error("失敗したことが画面に出ない")
	}
}

// 待っているあいだの表示は、残り時間を秒で出す。
func TestRematchStatusLines(t *testing.T) {
	cfg := smallConfig()
	rm := &fakeRematcher{fixedOpponent: fixedOpponent{c: rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}}}
	s := NewPlayStateWithSeat(cfg, rm, rules.P1)
	finish(t, s, cfg)

	if got := s.RematchStatus(); len(got) != 0 {
		t.Errorf("押す前から %v が出ている", got)
	}
	if err := s.RequestRematch(); err != nil {
		t.Fatalf("RequestRematch: %v", err)
	}
	rm.remaining, rm.hasRem = 41500*time.Millisecond, true
	got := s.RematchStatus()
	if len(got) != 2 {
		t.Fatalf("待ち表示 = %v, 期待 2行", got)
	}
	if got[1] != "のこり 42秒" {
		t.Errorf("残り時間の行 = %q, 期待 %q（四捨五入）", got[1], "のこり 42秒")
	}
}
