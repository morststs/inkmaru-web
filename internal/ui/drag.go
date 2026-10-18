package ui

import (
	"image"
	"math"

	"github.com/morststs/inkmaru-web/internal/rules"
)

// DragCircle はドラッグの始点と現在位置から円を作る。
// 始点が円の中心になり、始点から現在位置までの距離が半径になる。
// 半径と中心は Config の範囲に丸められるため、指を離すのが早すぎても
// 画面外に出てもターンを無駄にしない。
func DragCircle(cfg rules.Config, board image.Rectangle, sx, sy, cx, cy int) rules.Circle {
	side := float64(board.Dx())
	if side <= 0 {
		side = 1
	}
	nx := (float64(sx) - float64(board.Min.X)) / side
	ny := (float64(sy) - float64(board.Min.Y)) / side
	dx := float64(cx - sx)
	dy := float64(cy - sy)
	r := math.Hypot(dx, dy) / side
	return cfg.ClampCircle(rules.Circle{CX: nx, CY: ny, R: r})
}

// BoardToScreen は正規化座標を盤面領域内の画面座標に変換する。
func BoardToScreen(board image.Rectangle, nx, ny float64) (float32, float32) {
	side := float64(board.Dx())
	return float32(float64(board.Min.X) + nx*side), float32(float64(board.Min.Y) + ny*side)
}

// RadiusToScreen は正規化半径を画面上のピクセル半径に変換する。
func RadiusToScreen(board image.Rectangle, r float64) float32 {
	return float32(r * float64(board.Dx()))
}
