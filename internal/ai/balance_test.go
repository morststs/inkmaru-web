package ai

import (
	"math"
	"testing"

	"github.com/morststs/inkmaru-web/internal/rules"
)

// balanceGames はバランス計測の対戦数。先手後手を入れ替えて偏りを消す。
const balanceGames = 12

// balanceJitter は評価値に混ぜる雑音の割合。
// 0 にすると両者が全く同じ最善手を選び、鏡合わせの人工物が出るうえ、
// 何戦やっても結果が2通りに縮退して標本数が見せかけになる。
const balanceJitter = 0.10

// fixedPolicy は常に同じ半径を打つ Chooser を作る。バランス計測専用。
func fixedPolicy(r float64) Chooser {
	return &greedy{positions: 12, radii: 1, fixedR: r, jitter: balanceJitter}
}

func radiusForArea(area float64) float64 { return math.Sqrt(area / math.Pi) }

// territoryMargin は a の陣地率から b の陣地率を引いた値を、
// 先手後手を入れ替えながら平均する。正なら a が有利。
//
// 勝敗数ではなく陣地の差を測るのは、連続量のほうが感度が高く、
// 引き分けが多い戦法が「勝っていない」だけで健全に見えてしまう問題も避けられるため。
func territoryMargin(cfg rules.Config, a, b Chooser) float64 {
	sum := 0.0
	for i := 0; i < balanceGames; i++ {
		if i%2 == 0 {
			s := PlayMatchBoard(cfg, a, b, int64(i)).Score()
			sum += s.Ratio(rules.P1) - s.Ratio(rules.P2)
		} else {
			s := PlayMatchBoard(cfg, b, a, int64(i)).Score()
			sum += s.Ratio(rules.P2) - s.Ratio(rules.P1)
		}
	}
	return sum / float64(balanceGames)
}

// 仕様書 §4。「常に最大の円を描く」が支配戦略になっている設定はゲームとして破綻している。
//
// 面積が 1/3 の挑戦者と戦わせる。半径比は 0.577 で、小さい円が重なりを奪い返して
// 得をする境界（0.707）を下回っているため、両者は純粋に面積で殴り合う関係になる。
// そこで勝敗を分けるのは「大きい円が自分の前の円と重なってインクを無駄にするか」だけであり、
// これは MaxArea が盤面に対して十分大きいときにだけ起きる。
//
// 実測した陣地差（負が健全）:
//
//	MaxArea 0.08 -> +0.087 / 0.18 -> +0.193 / 0.32 -> +0.117
//	MaxArea 0.40 -> +0.014 / 0.47 -> -0.073 / 0.55 -> -0.173
//
// MaxArea を 0.40 以下に下げるとこのテストは落ちる。
func TestLargestCircleIsNotDominant(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 128
	challenger := radiusForArea(cfg.MaxArea / 3)

	m := territoryMargin(cfg, fixedPolicy(cfg.MaxRadius()), fixedPolicy(challenger))
	t.Logf("常に最大 vs 面積1/3の挑戦者: 陣地差 %+.3f", m)
	if m > 0 {
		t.Errorf("「常に最大の円」の陣地差 = %+.3f。支配戦略になっており MaxArea の設定が壊れている", m)
	}
}

// 逆方向の縮退。「常に最小の円」も支配戦略であってはならない。
//
// 実測した陣地差（負が健全）:
//
//	MinArea 0.03 -> -0.118 / 0.10 -> -0.152 / 0.14 -> -0.002
//	MinArea 0.16 -> +0.067 / 0.20 -> +0.256
//
// MinArea を 0.16 以上に上げるとこのテストは落ちる。
func TestSmallestCircleIsNotDominant(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 128
	challenger := radiusForArea(cfg.MinArea * 3)

	m := territoryMargin(cfg, fixedPolicy(cfg.MinRadius()), fixedPolicy(challenger))
	t.Logf("常に最小 vs 面積3倍の挑戦者: 陣地差 %+.3f", m)
	if m > 0 {
		t.Errorf("「常に最小の円」の陣地差 = %+.3f。支配戦略になっており MinArea の設定が壊れている", m)
	}
}
