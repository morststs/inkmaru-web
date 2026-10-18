package ai

import (
	"math"
	"math/rand"
	"testing"

	"github.com/morststs/inkmaru-web/internal/rules"
)

func TestChooseReturnsValidCircle(t *testing.T) {
	cfg := rules.DefaultConfig()
	for _, d := range []Difficulty{Easy, Normal, Hard} {
		c := New(d).Choose(nil, cfg, rules.P1, 0, rand.New(rand.NewSource(1)))
		if c.CX < 0 || c.CX > 1 || c.CY < 0 || c.CY > 1 {
			t.Errorf("%v: 中心が盤外 %+v", d, c)
		}
		if c.R < cfg.MinRadius()-1e-9 || c.R > cfg.MaxRadius()+1e-9 {
			t.Errorf("%v: 半径がレンジ外 %v", d, c.R)
		}
	}
}

func TestChooseIsDeterministicForSameSeed(t *testing.T) {
	// Easy を使う。Normal/Hard の jitter は 0 であり、jitter==0 の greedy.Choose は
	// rnd に一切触れない（rnd.Float64() の呼び出しが if g.jitter > 0 の中にしかない）。
	// つまり jitter を持たない難易度で「同じ乱数種で同じ結果」を検証しても、
	// 乱数の種の受け渡し（シード配線）は何も検証できていない
	// （rnd.Float64() を大域の rand.Float64() に差し替える退行を入れても、
	// jitter=0 の経路は変わらず素通りしてグリーンのままになる）。
	// Easy（jitter=0.5）だけが実際に rnd を消費するため、ここでは Easy を使う。
	//
	// また、盤面が空の状態で Choose を1回呼ぶだけでは、候補間の評価値の差が
	// 大きすぎて jitter をどれだけ振っても最良手が変わらないことがあり、
	// それでは大域乱数への差し替えを見逃してしまう。そこで Easy 同士で
	// 3ターン分の対戦を最後まで行い、確定した手の履歴がまるごと一致するかを見る。
	// ターンを重ねるほど盤面が複雑になって評価値が拮抗する候補が増え、
	// また Choose の呼び出し回数（=乱数消費回数）も増えるため、
	// 乱数源の取り違えがあれば高い確率でどこかの手がズレて検出できる。
	cfg := rules.DefaultConfig()
	cfg.GridN = 64 // テストを速くする
	a := playMatchInternal(cfg, New(Easy), New(Easy), 42)
	b := playMatchInternal(cfg, New(Easy), New(Easy), 42)
	ha, hb := a.History(), b.History()
	if len(ha) != len(hb) {
		t.Fatalf("履歴の長さが違う: %d vs %d", len(ha), len(hb))
	}
	for i := range ha {
		if ha[i].Circle != hb[i].Circle {
			t.Errorf("手 %d が同じ乱数種で違う: %+v vs %+v", i, ha[i].Circle, hb[i].Circle)
		}
	}
}

func winRate(t *testing.T, cfg rules.Config, strong, weak Chooser, games int) float64 {
	t.Helper()
	wins := 0
	for i := 0; i < games; i++ {
		// 先手後手を入れ替えて偏りを消す。
		if i%2 == 0 {
			if PlayMatch(cfg, strong, weak, int64(i)) == rules.WinP1 {
				wins++
			}
		} else {
			if PlayMatch(cfg, weak, strong, int64(i)) == rules.WinP2 {
				wins++
			}
		}
	}
	return float64(wins) / float64(games)
}

func TestNormalBeatsEasy(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 128 // テストを速くする
	if r := winRate(t, cfg, New(Normal), New(Easy), 20); r < 0.7 {
		t.Errorf("Normal の対 Easy 勝率 = %.2f, 期待 0.70 以上", r)
	}
}

func TestHardBeatsNormal(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 128
	if r := winRate(t, cfg, New(Hard), New(Normal), 20); r < 0.6 {
		t.Errorf("Hard の対 Normal 勝率 = %.2f, 期待 0.60 以上", r)
	}
}

func TestFixedRadiusChooserRespectsRadius(t *testing.T) {
	cfg := rules.DefaultConfig()
	c := FixedRadius(cfg.MaxRadius()).Choose(nil, cfg, rules.P1, 0, rand.New(rand.NewSource(3)))
	if math.Abs(c.R-cfg.MaxRadius()) > 1e-9 {
		t.Errorf("半径 = %v, 期待 %v", c.R, cfg.MaxRadius())
	}
}
