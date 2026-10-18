package rules

import (
	"math"
	"math/rand"
	"testing"
)

func TestApplySingleCircleAreaMatchesTheory(t *testing.T) {
	c := DefaultConfig()
	b := NewBoard(c)
	r := 0.2
	b.Apply(Circle{CX: 0.5, CY: 0.5, R: r}, P1, c.InkPerTurn)

	want := math.Pi * r * r * float64(c.GridN*c.GridN)
	got := float64(b.Score().Cells[P1])
	if math.Abs(got-want)/want > 0.03 {
		t.Errorf("所有セル数 = %v, 理論値 %v（許容3%%）", got, want)
	}
}

func TestSmallCircleOverwritesLargeCircle(t *testing.T) {
	c := DefaultConfig()
	b := NewBoard(c)
	b.Apply(Circle{0.5, 0.5, c.MaxRadius()}, P1, c.InkPerTurn) // 薄い
	b.Apply(Circle{0.5, 0.5, c.MinRadius()}, P2, c.InkPerTurn) // 濃い

	if got := b.OwnerAt(c.GridN/2, c.GridN/2); got != P2 {
		t.Errorf("中心の所有者 = %v, 期待 P2（濃いほうが勝つ）", got)
	}
	// 小円の外側は P1 のまま残る。
	if got := b.OwnerAt(c.GridN/2, c.GridN/2+int(c.MinRadius()*float64(c.GridN))+4); got != P1 {
		t.Errorf("小円の外側の所有者 = %v, 期待 P1", got)
	}
}

func TestEqualDensityDifferentPlayersBecomesNeutral(t *testing.T) {
	c := DefaultConfig()
	b := NewBoard(c)
	b.Apply(Circle{0.5, 0.5, 0.2}, P1, c.InkPerTurn)
	b.Apply(Circle{0.5, 0.5, 0.2}, P2, c.InkPerTurn)

	if got := b.OwnerAt(c.GridN/2, c.GridN/2); got != Neutral {
		t.Errorf("所有者 = %v, 期待 Neutral（同じ濃さは中立）", got)
	}
	if s := b.Score(); s.Cells[P1] != 0 || s.Cells[P2] != 0 {
		t.Errorf("相討ちなのに陣地が残っている: %+v", s.Cells)
	}
}

func TestSameOwnerCirclesMerge(t *testing.T) {
	c := DefaultConfig()
	b := NewBoard(c)
	b.Apply(Circle{0.5, 0.5, 0.2}, P1, c.InkPerTurn)
	b.Apply(Circle{0.5, 0.5, 0.2}, P1, c.InkPerTurn)

	if got := b.OwnerAt(c.GridN/2, c.GridN/2); got != P1 {
		t.Errorf("所有者 = %v, 期待 P1（自分の円どうしは中立にならない）", got)
	}
}

// 仕様書 §3.4 の不変条件。maxD と mask だけを保持する表現がこれを保証する。
func TestApplyOrderIndependence(t *testing.T) {
	c := DefaultConfig()
	c.GridN = 64
	type move struct {
		ci Circle
		p  Player
	}
	rnd := rand.New(rand.NewSource(7))
	for trial := 0; trial < 20; trial++ {
		moves := make([]move, 6)
		for i := range moves {
			p := P1
			if i%2 == 1 {
				p = P2
			}
			// 半径は数種類から選び、同濃度の衝突が起きるようにする。
			radii := []float64{c.MinRadius(), 0.15, 0.25, c.MaxRadius()}
			moves[i] = move{Circle{rnd.Float64(), rnd.Float64(), radii[rnd.Intn(len(radii))]}, p}
		}
		ref := NewBoard(c)
		for _, m := range moves {
			ref.Apply(m.ci, m.p, c.InkPerTurn)
		}
		for shuffle := 0; shuffle < 5; shuffle++ {
			perm := rnd.Perm(len(moves))
			got := NewBoard(c)
			for _, i := range perm {
				got.Apply(moves[i].ci, moves[i].p, c.InkPerTurn)
			}
			for y := 0; y < c.GridN; y++ {
				for x := 0; x < c.GridN; x++ {
					if got.OwnerAt(x, y) != ref.OwnerAt(x, y) {
						t.Fatalf("順序で結果が変わった trial=%d cell=(%d,%d): %v != %v",
							trial, x, y, got.OwnerAt(x, y), ref.OwnerAt(x, y))
					}
				}
			}
		}
	}
}

func TestOffBoardInkIsLost(t *testing.T) {
	c := DefaultConfig()
	centre := NewBoard(c)
	centre.Apply(Circle{0.5, 0.5, 0.2}, P1, c.InkPerTurn)
	corner := NewBoard(c)
	corner.Apply(Circle{0, 0, 0.2}, P1, c.InkPerTurn)

	// 角に置くと円の約 1/4 しか盤面に乗らない。
	got, want := corner.Score().Cells[P1], centre.Score().Cells[P1]/4
	if math.Abs(float64(got-want))/float64(want) > 0.05 {
		t.Errorf("角の所有セル数 = %d, 期待 約%d", got, want)
	}
}

func TestScoreCellsSumToTotal(t *testing.T) {
	c := DefaultConfig()
	b := NewBoard(c)
	b.Apply(Circle{0.3, 0.3, 0.25}, P1, c.InkPerTurn)
	b.Apply(Circle{0.6, 0.6, 0.25}, P2, c.InkPerTurn)
	b.Apply(Circle{0.5, 0.5, 0.12}, P1, c.InkPerTurn)

	s := b.Score()
	if sum := s.Cells[None] + s.Cells[P1] + s.Cells[P2] + s.Cells[Neutral]; sum != s.Total {
		t.Errorf("セル数の合計 = %d, 期待 %d", sum, s.Total)
	}
}

// TestShareMatchesRatioWhenDensityWeightIsZero は、既定の DensityWeight=0 の下では
// Share が Ratio と完全に一致することを保証する。今日の表示（Ratio 相当）を
// 壊さないことの直接的な証明。
func TestShareMatchesRatioWhenDensityWeightIsZero(t *testing.T) {
	c := DefaultConfig()
	c.GridN = 64
	b := NewBoard(c)
	b.Apply(Circle{0.3, 0.3, 0.25}, P1, c.InkPerTurn)
	b.Apply(Circle{0.6, 0.6, 0.25}, P2, c.InkPerTurn)
	b.Apply(Circle{0.5, 0.5, 0.12}, P1, c.InkPerTurn)

	s := b.Score()
	for _, p := range []Player{None, P1, P2, Neutral} {
		if got, want := s.Share(p), s.Ratio(p); math.Abs(got-want) > 1e-12 {
			t.Errorf("Player %v: Share = %v, Ratio = %v（DensityWeight=0 では一致するはず）", p, got, want)
		}
	}
}

// TestShareAgreesWithMatchResult は「勝敗が Weighted の大小で決まる」ことと
// 「表示は Share を使う」ことが food-for-thought どころか実際に食い違わないことを、
// DensityWeight=0.5 という CLAUDE.md が推奨する値そのもので検証する。
// これは §2 で見つかった罠（表示は Ratio、判定は Weighted で、両者がズレる）の
// 再発防止テストである。
func TestShareAgreesWithMatchResult(t *testing.T) {
	c := DefaultConfig()
	c.GridN = 64
	c.DensityWeight = 0.5
	c.Turns = 3

	rnd := rand.New(rand.NewSource(11))
	for trial := 0; trial < 30; trial++ {
		m := NewMatch(c)
		for !m.Finished() {
			r1 := c.MinRadius() + rnd.Float64()*(c.MaxRadius()-c.MinRadius())
			r2 := c.MinRadius() + rnd.Float64()*(c.MaxRadius()-c.MinRadius())
			m.Submit(P1, Circle{rnd.Float64(), rnd.Float64(), r1})
			m.Submit(P2, Circle{rnd.Float64(), rnd.Float64(), r2})
			m.Resolve()
		}
		s := m.Board().Score()
		sh1, sh2 := s.Share(P1), s.Share(P2)
		result := m.Result()

		switch {
		case sh1 > sh2 && result != WinP1:
			t.Fatalf("trial %d: Share(P1)=%v > Share(P2)=%v なのに結果は %v（期待 WinP1）", trial, sh1, sh2, result)
		case sh2 > sh1 && result != WinP2:
			t.Fatalf("trial %d: Share(P2)=%v > Share(P1)=%v なのに結果は %v（期待 WinP2）", trial, sh2, sh1, result)
		case sh1 == sh2 && result != Draw:
			t.Fatalf("trial %d: Share(P1)==Share(P2)==%v なのに結果は %v（期待 Draw）", trial, sh1, result)
		}
	}
}

func TestCloneIsIndependent(t *testing.T) {
	c := DefaultConfig()
	b := NewBoard(c)
	b.Apply(Circle{0.5, 0.5, 0.2}, P1, c.InkPerTurn)
	cp := b.Clone()
	cp.Apply(Circle{0.5, 0.5, c.MinRadius()}, P2, c.InkPerTurn)

	if b.OwnerAt(c.GridN/2, c.GridN/2) != P1 {
		t.Error("Clone への Apply が元の盤面を書き換えている")
	}
}
