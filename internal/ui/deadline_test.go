package ui

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/morststs/inkmaru-web/internal/ai"
	"github.com/morststs/inkmaru-web/internal/rules"
)

// deadlineStub は制限時間を持つ相手。オンライン対戦の NetOpponent を模す。
type deadlineStub struct {
	stubOpponent
	now      time.Time
	deadline time.Time
	anchored bool
}

func (d *deadlineStub) Now() time.Time                 { return d.now }
func (d *deadlineStub) Deadline(int) (time.Time, bool) { return d.deadline, d.anchored }

func newDeadlineState(t *testing.T) (*PlayState, *deadlineStub, time.Time) {
	t.Helper()
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	base := time.Unix(1_700_000_000, 0)
	stub := &deadlineStub{
		now:      base,
		deadline: base.Add(30 * time.Second),
		anchored: true,
	}
	stub.reply = rules.Circle{CX: 0.8, CY: 0.8, R: 0.1}
	stub.ready = true
	return NewPlayStateWith(cfg, stub), stub, base
}

// 制限時間を過ぎたら、描いてある円でプレイヤーの代わりに確定する。
// 出す円は必ず Confirm を通る。wire に出る円を決める経路が2本あると
// 「自分の盤面の円 ≠ 相手に送った円」が起きて board-mismatch になる。
func TestDeadlineAutoConfirmsDrawnPreview(t *testing.T) {
	s, stub, base := newDeadlineState(t)
	mine := rules.Circle{CX: 0.2, CY: 0.2, R: 0.25}
	s.SetPreview(mine)

	stub.now = base.Add(31 * time.Second)
	s.Tick(0.016)

	if s.Phase() == PhaseDrawing {
		t.Fatal("時間切れなのに描画フェーズのままである")
	}
	if len(stub.got) != 1 {
		t.Fatalf("提出された手の数 = %d, 期待 1", len(stub.got))
	}
	if stub.got[0] != s.Match().Config().ClampCircle(mine) {
		t.Errorf("提出された円 = %v, 期待 %v（描いてあった円）", stub.got[0], mine)
	}
}

// 何も描いていないまま時間切れになったら最小半径の円を出す（仕様書 §7.1）。
// 位置は時計から導くので、時計を固定したテストでは決定的である。
func TestDeadlineAutoConfirmsMinimumCircleWhenNothingDrawn(t *testing.T) {
	s, stub, base := newDeadlineState(t)

	stub.now = base.Add(31 * time.Second)
	s.Tick(0.016)

	if s.Phase() == PhaseDrawing {
		t.Fatal("時間切れなのに描画フェーズのままである")
	}
	if len(stub.got) != 1 {
		t.Fatalf("提出された手の数 = %d, 期待 1", len(stub.got))
	}
	cfg := s.Match().Config()
	got := stub.got[0]
	if got.R > cfg.MinRadius()+1e-9 {
		t.Errorf("自動確定の半径 = %v, 期待 最小 %v", got.R, cfg.MinRadius())
	}
	if got.CX < 0 || got.CX > 1 || got.CY < 0 || got.CY > 1 {
		t.Errorf("自動確定の中心が盤面外: %v", got)
	}
	// プレイヤーが何が出されたかをリビールで見られる必要がある。
	if mv := s.LastMoves(); mv[0] != got { // 添字0が自分（P1）の手
		t.Errorf("リビールに映る自分の円 = %v, 期待 %v", mv[0], got)
	}
}

func TestDeadlineDoesNotFireBeforeExpiry(t *testing.T) {
	s, stub, base := newDeadlineState(t)
	s.SetPreview(rules.Circle{CX: 0.2, CY: 0.2, R: 0.25})

	stub.now = base.Add(29 * time.Second)
	for i := 0; i < 60; i++ {
		s.Tick(0.016)
	}
	if s.Phase() != PhaseDrawing {
		t.Fatalf("期限前なのにフェーズが %v になった", s.Phase())
	}
	if len(stub.got) != 0 {
		t.Errorf("期限前に %d 件提出された", len(stub.got))
	}
}

// 起点がまだ立っていないターンは「すでに時間切れ」と読んではならない。
// ゼロ値の time.Time は西暦1年なので、素朴に比較すると初回フレームで
// 即座に自動確定してしまう。
func TestUnanchoredDeadlineNeverAutoConfirms(t *testing.T) {
	s, stub, base := newDeadlineState(t)
	stub.anchored = false
	stub.deadline = time.Time{}

	stub.now = base.Add(10 * time.Hour)
	for i := 0; i < 60; i++ {
		s.Tick(0.016)
	}
	if s.Phase() != PhaseDrawing {
		t.Fatalf("起点が無いのにフェーズが %v になった", s.Phase())
	}
	if len(stub.got) != 0 {
		t.Errorf("起点が無いのに %d 件提出された", len(stub.got))
	}
}

// CPU 対戦には制限時間が無い。cpuOpponent は Deadline を実装しないので、
// 型アサーションが外れて第1段階の挙動がそのまま残る。
func TestCPUOpponentHasNoDeadline(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	s := NewPlayStateWith(cfg, NewCPUOpponent(ai.Normal, 1))
	for i := 0; i < 600; i++ { // 10秒相当
		s.Tick(0.016)
	}
	if s.Phase() != PhaseDrawing {
		t.Errorf("CPU 対戦のフェーズが %v になった。期待 PhaseDrawing", s.Phase())
	}
	if s.Match().Turn() != 0 {
		t.Errorf("CPU 対戦のターンが %d に進んだ", s.Match().Turn())
	}
}

func TestRemainingCountsDown(t *testing.T) {
	s, stub, base := newDeadlineState(t)
	stub.now = base.Add(10 * time.Second)
	got, ok := s.Remaining()
	if !ok {
		t.Fatal("残り時間が取れなかった")
	}
	if got != 20*time.Second {
		t.Errorf("残り時間 = %v, 期待 20s", got)
	}

	// 期限を過ぎても負にはしない。表示がマイナスになると意味が分からない。
	stub.now = base.Add(45 * time.Second)
	if got, _ := s.Remaining(); got != 0 {
		t.Errorf("期限後の残り時間 = %v, 期待 0", got)
	}

	stub.anchored = false
	if _, ok := s.Remaining(); ok {
		t.Error("起点が無いのに残り時間が取れてしまった")
	}
}

func TestRemainingIsAbsentForCPU(t *testing.T) {
	s := newTestState()
	if _, ok := s.Remaining(); ok {
		t.Error("CPU 対戦で残り時間が取れてしまった")
	}
}

// peerFaultStub は「相手側の落ち度で終わった」エラーを返す相手。
type peerFaultErr struct {
	reason string
	cause  error
}

func (e peerFaultErr) Error() string           { return e.reason + ": " + e.cause.Error() }
func (e peerFaultErr) Unwrap() []error         { return []error{ErrPeerGone, e.cause} }
func (e peerFaultErr) PeerFaultReason() string { return e.reason }

// 相手側の落ち度による終了は「自分の勝ち」でありながら理由も残る。
// 勝敗（誰が）と理由（なぜ）は別の軸であり、結果画面は両方を出せる必要がある。
func TestPeerFaultIsAWinAndKeepsItsReason(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	stub := &stubOpponent{err: peerFaultErr{reason: "hash-mismatch", cause: errors.New("原因")}}
	s := NewPlayStateWith(cfg, stub)

	s.SetPreview(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2})
	_ = s.Confirm()
	s.Tick(0.016)

	if s.Phase() != PhaseFinished {
		t.Fatalf("フェーズ = %v, 期待 PhaseFinished", s.Phase())
	}
	if !s.PeerLeft() {
		t.Error("相手側の落ち度なのに自分の勝ちになっていない")
	}
	if s.AbortReason() == "" {
		t.Error("理由が残っていない")
	}
}

// 理由の無い素の切断では、これまでどおり AbortReason は空のままである。
//
// 理由が空の PeerFault も含めて検査する。オンラインの実装は素の切断も
// PeerFault で包んで返す（理由だけが空）ので、「PeerFault なら理由を入れる」
// ではなく「PeerFault が持つ理由をそのまま使う」でなければ、素の切断の
// 結果画面にエラー文字列が出てしまう。
func TestPlainPeerGoneHasNoReason(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	cases := map[string]error{
		"番兵を包んだだけ":        fmt.Errorf("包み: %w", ErrPeerGone),
		"理由が空の PeerFault": peerFaultErr{reason: "", cause: ErrPeerGone},
	}
	for name, err := range cases {
		t.Run(name, func(t *testing.T) {
			stub := &stubOpponent{err: err}
			s := NewPlayStateWith(cfg, stub)

			s.SetPreview(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2})
			_ = s.Confirm()
			s.Tick(0.016)

			if !s.PeerLeft() {
				t.Error("切断が自分の勝ちになっていない")
			}
			if s.AbortReason() != "" {
				t.Errorf("AbortReason = %q, 期待 空", s.AbortReason())
			}
		})
	}
}

// 自分側の失敗（送信の変換に失敗した等）は勝敗をつけない。
// ここまで勝ちにすると、自分のバグで勝ってしまう。
func TestLocalFailureIsNotAWin(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	stub := &stubOpponent{err: errors.New("netplay: 送信の変換に失敗")}
	s := NewPlayStateWith(cfg, stub)

	s.SetPreview(rules.Circle{CX: 0.5, CY: 0.5, R: 0.2})
	_ = s.Confirm()
	s.Tick(0.016)

	if s.PeerLeft() {
		t.Error("自分側の失敗で勝ちになっている")
	}
	if s.AbortReason() == "" {
		t.Error("中断の理由が残っていない")
	}
}
