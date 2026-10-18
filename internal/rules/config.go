// Package rules は Inkmaru のゲームルールを実装する。
// このパッケージは描画エンジンに依存しない。CPU AI とバランス調整用の
// シミュレーションが同じ実装を共有できるようにするためである。
package rules

import "math"

// Config はゲームのルールを決めるパラメータ一式。
type Config struct {
	GridN                  int     // 陣地判定の格子の一辺
	Turns                  int     // ターン数
	InkPerTurn             float64 // 1ターンに使えるインク量
	MinArea                float64 // 最小の円が盤面に占める割合
	MaxArea                float64 // 最大の円が盤面に占める割合
	DensityWeight          float64 // 得点の濃さ重み。0 なら所有セル数そのもの
	FinalTurnInkMultiplier float64 // 最終ターンのインク倍率。1.0 で無効
}

// DefaultConfig は仕様書 §3 で確定した既定値を返す。
// MaxArea は最も影響が大きい。ここを下げすぎると「常に最大の円を描く」が
// 支配戦略になりゲームが壊れる（仕様書 §4）。
func DefaultConfig() Config {
	return Config{
		GridN:                  256,
		Turns:                  3,
		InkPerTurn:             0.47,
		MinArea:                0.03,
		MaxArea:                0.47,
		DensityWeight:          0,
		FinalTurnInkMultiplier: 1.0,
	}
}

// MinRadius は最小の円の半径。
func (c Config) MinRadius() float64 { return math.Sqrt(c.MinArea / math.Pi) }

// MaxRadius は最大の円の半径。
func (c Config) MaxRadius() float64 { return math.Sqrt(c.MaxArea / math.Pi) }

// InkForTurn は turn（0始まり）で使えるインク量を返す。
func (c Config) InkForTurn(turn int) float64 {
	if turn == c.Turns-1 {
		return c.InkPerTurn * c.FinalTurnInkMultiplier
	}
	return c.InkPerTurn
}

// Circle は盤面に描く円。座標は [0,1] の正規化座標。
type Circle struct{ CX, CY, R float64 }

// ClampCircle は中心を盤面内に、半径を面積レンジ内に丸める。
func (c Config) ClampCircle(ci Circle) Circle {
	ci.CX = clamp(ci.CX, 0, 1)
	ci.CY = clamp(ci.CY, 0, 1)
	ci.R = clamp(ci.R, c.MinRadius(), c.MaxRadius())
	return ci
}

// Density は半径 r の円にインク量 ink を広げたときの濃さを返す。
// 大きく描くほど薄くなるのはこの式による。
func Density(ink, r float64) float64 { return ink / (math.Pi * r * r) }

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
