package rules

import (
	"errors"
	"math"
	"testing"
)

func TestSubmitRejectsDoubleSubmission(t *testing.T) {
	m := NewMatch(DefaultConfig())
	if err := m.Submit(P1, Circle{0.5, 0.5, 0.2}); err != nil {
		t.Fatalf("1回目の提出が失敗した: %v", err)
	}
	if err := m.Submit(P1, Circle{0.3, 0.3, 0.2}); !errors.Is(err, ErrAlreadySubmitted) {
		t.Errorf("2回目の提出のエラー = %v, 期待 ErrAlreadySubmitted", err)
	}
}

func TestResolveWaitsForBothPlayers(t *testing.T) {
	m := NewMatch(DefaultConfig())
	m.Submit(P1, Circle{0.5, 0.5, 0.2})
	if m.Ready() {
		t.Error("片方しか提出していないのに Ready が true")
	}
	if _, ok := m.Resolve(); ok {
		t.Error("片方しか提出していないのに Resolve が成功した")
	}
	if m.Turn() != 0 {
		t.Errorf("ターンが進んでしまった: %d", m.Turn())
	}
	m.Submit(P2, Circle{0.3, 0.3, 0.2})
	if _, ok := m.Resolve(); !ok {
		t.Fatal("両者提出後に Resolve が失敗した")
	}
	if m.Turn() != 1 {
		t.Errorf("ターン = %d, 期待 1", m.Turn())
	}
}

func TestMatchFinishesAfterConfiguredTurns(t *testing.T) {
	c := DefaultConfig()
	m := NewMatch(c)
	for i := 0; i < c.Turns; i++ {
		if m.Finished() {
			t.Fatalf("ターン %d で既に終了している", i)
		}
		m.Submit(P1, Circle{0.35, 0.35, 0.2})
		m.Submit(P2, Circle{0.65, 0.65, 0.2})
		m.Resolve()
	}
	if !m.Finished() {
		t.Fatal("規定ターン後に終了していない")
	}
	if err := m.Submit(P1, Circle{0.5, 0.5, 0.2}); !errors.Is(err, ErrMatchFinished) {
		t.Errorf("終了後の提出のエラー = %v, 期待 ErrMatchFinished", err)
	}
	if len(m.History()) != c.Turns*2 {
		t.Errorf("履歴の件数 = %d, 期待 %d", len(m.History()), c.Turns*2)
	}
}

func TestResultReflectsTerritory(t *testing.T) {
	c := DefaultConfig()
	c.Turns = 1
	m := NewMatch(c)
	m.Submit(P1, Circle{0.5, 0.5, c.MaxRadius()}) // 広い
	m.Submit(P2, Circle{0.1, 0.1, c.MinRadius()}) // 狭い
	m.Resolve()
	if got := m.Result(); got != WinP1 {
		t.Errorf("結果 = %v, 期待 WinP1", got)
	}
}

func TestResultDrawOnMirroredPlay(t *testing.T) {
	c := DefaultConfig()
	c.Turns = 1
	m := NewMatch(c)
	// 盤面の中心について対称に置けば陣地は同数になる。
	m.Submit(P1, Circle{0.25, 0.5, 0.2})
	m.Submit(P2, Circle{0.75, 0.5, 0.2})
	m.Resolve()
	if got := m.Result(); got != Draw {
		s := m.Board().Score()
		t.Errorf("結果 = %v, 期待 Draw (P1=%d P2=%d)", got, s.Cells[P1], s.Cells[P2])
	}
}

func TestInProgressBeforeFinish(t *testing.T) {
	m := NewMatch(DefaultConfig())
	if got := m.Result(); got != InProgress {
		t.Errorf("結果 = %v, 期待 InProgress", got)
	}
}

func TestBuildBoardMatchesIncrementalApply(t *testing.T) {
	c := DefaultConfig()
	c.GridN = 64
	hist := []Move{
		{Circle{0.3, 0.3, 0.25}, P1, c.InkPerTurn},
		{Circle{0.6, 0.6, 0.25}, P2, c.InkPerTurn},
		{Circle{0.5, 0.5, 0.12}, P1, c.InkPerTurn},
	}
	want := NewBoard(c)
	for _, mv := range hist {
		want.Apply(mv.Circle, mv.Player, mv.Ink)
	}
	got := BuildBoard(c, hist)
	if got.Score() != want.Score() {
		t.Errorf("BuildBoard の結果が Apply の積み上げと一致しない")
	}
}

func TestSubmitClampsCircle(t *testing.T) {
	c := DefaultConfig()
	m := NewMatch(c)
	m.Submit(P1, Circle{-1, -1, 999})
	m.Submit(P2, Circle{0.5, 0.5, 0.2})
	got, _ := m.Resolve()
	if got[0].R > c.MaxRadius()+1e-12 || got[0].CX < 0 {
		t.Errorf("提出時に正規化されていない: %+v", got[0])
	}
}

// TestResolveAppliesFinalTurnInkMultiplier は、Match.Resolve が実際に
// Config.InkForTurn（FinalTurnInkMultiplier を反映する）を呼んでいることを保証する。
// config_test.go の TestInkForTurnUsesFinalMultiplier は InkForTurn 単体のロジックしか
// 検証しておらず、Resolve が InkForTurn を呼ばず素の InkPerTurn を使う退行に
// 差し替えても検出できない（実際にレビューで指摘された）。
// 両プレイヤーが毎ターン同じ円を同じ場所に描く対戦を組み、最終ターンだけ
// インク倍率を変えた2つの対戦を比較する。同じ円を重ねて描くと、そのセルの
// 濃度は「これまでに見た最大濃度」のまま据え置かれるため、最終ターンの
// インク量が変われば最終ターンにその円が塗る濃度も変わり、盤面の maxD に
// 直接反映される。
func TestResolveAppliesFinalTurnInkMultiplier(t *testing.T) {
	play := func(mult float64) *Board {
		c := DefaultConfig()
		c.GridN = 64
		c.FinalTurnInkMultiplier = mult
		m := NewMatch(c)
		p1Circle := Circle{CX: 0.3, CY: 0.3, R: 0.2}
		p2Circle := Circle{CX: 0.7, CY: 0.7, R: 0.2}
		for !m.Finished() {
			if err := m.Submit(P1, p1Circle); err != nil {
				t.Fatalf("P1 提出が失敗した: %v", err)
			}
			if err := m.Submit(P2, p2Circle); err != nil {
				t.Fatalf("P2 提出が失敗した: %v", err)
			}
			if _, ok := m.Resolve(); !ok {
				t.Fatal("Resolve が失敗した")
			}
		}
		return m.Board()
	}

	c := DefaultConfig()
	c.GridN = 64
	gx := int(0.3 * float64(c.GridN))
	gy := int(0.3 * float64(c.GridN))

	boardNormal := play(1.0)
	boardBoosted := play(2.0)

	dNormal := boardNormal.DensityAt(gx, gy)
	dBoosted := boardBoosted.DensityAt(gx, gy)

	if dBoosted <= dNormal {
		t.Fatalf("FinalTurnInkMultiplier=2.0 の盤面の濃度 = %v, 等倍(%v)以下になっている（Resolve が InkForTurn を使っていない疑い）",
			dBoosted, dNormal)
	}
	// 毎ターン同じ円・同じインク量なら maxD は据え置かれる。乗数2.0の対戦だけ
	// 最終ターンの濃度がおよそ2倍になるはず。
	want := dNormal * 2
	if diff := math.Abs(dBoosted-want) / want; diff > 0.02 {
		t.Errorf("FinalTurnInkMultiplier=2.0 の濃度 = %v, 期待 %v 前後（等倍の2倍）", dBoosted, want)
	}
}

func TestHistoryExcludesUnresolvedMoves(t *testing.T) {
	// History() は確定・公開済みの手だけを返す。
	// 提出済みだが未確定の手は決して含まれない。
	// これが CPU に未確定の円を見させない保証であり、
	// internal/ui の TestCPUNeverSeesUnconfirmedCircle が依存している。

	c := DefaultConfig()
	m := NewMatch(c)

	// 最初は履歴が空
	if len(m.History()) != 0 {
		t.Errorf("初期状態の履歴の長さ = %d, 期待 0", len(m.History()))
	}

	// ターン0: P1 を提出
	c1 := Circle{0.3, 0.3, 0.2}
	m.Submit(P1, c1)
	if len(m.History()) != 0 {
		t.Errorf("P1 提出後の履歴の長さ = %d, 期待 0（未確定のため）", len(m.History()))
	}

	// P2 も提出
	c2 := Circle{0.7, 0.7, 0.2}
	m.Submit(P2, c2)
	if len(m.History()) != 0 {
		t.Errorf("P2 提出後の履歴の長さ = %d, 期待 0（未確定のため）", len(m.History()))
	}

	// Resolve ではじめて確定・公開
	m.Resolve()
	if len(m.History()) != 2 {
		t.Errorf("ターン0確定後の履歴の長さ = %d, 期待 2", len(m.History()))
	}

	// ターン1でも同じ性質が成り立つ
	c3 := Circle{0.4, 0.4, 0.2}
	m.Submit(P1, c3)
	if len(m.History()) != 2 {
		t.Errorf("ターン1 P1 提出後の履歴の長さ = %d, 期待 2（ターン1は未確定）", len(m.History()))
	}

	c4 := Circle{0.6, 0.6, 0.2}
	m.Submit(P2, c4)
	if len(m.History()) != 2 {
		t.Errorf("ターン1 P2 提出後の履歴の長さ = %d, 期待 2（ターン1は未確定）", len(m.History()))
	}

	m.Resolve()
	if len(m.History()) != 4 {
		t.Errorf("ターン1確定後の履歴の長さ = %d, 期待 4", len(m.History()))
	}
}
