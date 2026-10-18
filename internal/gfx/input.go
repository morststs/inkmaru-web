package gfx

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

// Pointer はマウスとタッチを同じ形で扱うための抽象。
type Pointer struct {
	X, Y         int
	Pressed      bool
	JustPressed  bool
	JustReleased bool
}

// ReadPointer は現在の入力状態を返す。
// buf は割り当てを避けるための作業領域で、呼び出し側が保持して渡し直す。
//
// 注意: 離された直後のタッチは ebiten.TouchPosition が (0,0) を返す。
// 指を離した位置が必要な場合は、押されている間の座標を呼び出し側が
// 覚えておく必要がある（scene_play.go の lastX/lastY）。
func ReadPointer(buf []ebiten.TouchID) (Pointer, []ebiten.TouchID) {
	var p Pointer

	buf = inpututil.AppendJustPressedTouchIDs(buf[:0])
	if len(buf) > 0 {
		p.X, p.Y = ebiten.TouchPosition(buf[0])
		p.Pressed, p.JustPressed = true, true
		return p, buf
	}
	buf = ebiten.AppendTouchIDs(buf[:0])
	if len(buf) > 0 {
		p.X, p.Y = ebiten.TouchPosition(buf[0])
		p.Pressed = true
		return p, buf
	}
	buf = inpututil.AppendJustReleasedTouchIDs(buf[:0])
	if len(buf) > 0 {
		p.JustReleased = true
		return p, buf
	}

	p.X, p.Y = ebiten.CursorPosition()
	p.Pressed = ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft)
	p.JustPressed = inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft)
	p.JustReleased = inpututil.IsMouseButtonJustReleased(ebiten.MouseButtonLeft)
	return p, buf
}
