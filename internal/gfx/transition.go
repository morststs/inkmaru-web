package gfx

import (
	"github.com/hajimehoshi/ebiten/v2"
)

// transitionSeconds は場面転換にかける秒数。
const transitionSeconds = 0.18

// transition は場面が切り替わるときのクロスフェード。
//
// 切り替わる瞬間に**切り替わる前の画面を1枚だけ**取っておき、それを
// 薄くしながら上に重ねる。前の画面を毎フレーム描き直さないので、
// 転換中でも描画の負荷は1枚ぶんしか増えない。
//
// **控えの画像は転換が終わったら捨てる。** 画面の大きさが変わるたびに
// 作り直すので、捨てないと古いテクスチャが GC のファイナライザ待ちで残る。
type transition struct {
	prev *ebiten.Image
	t    float64 // 0 から 1 へ。1 で転換が終わっている。初期値 0 は「まだ始まっていない」
	on   bool
}

// begin は転換を始める。lastFrame は**前フレームの絵をまだ持っている画像**
// （Game.frame。新しい場面を描く前に呼ぶこと）で、その中身を控える。
// ebiten の screen は渡さない。毎フレーム消され、描画元に使うことも
// 公式が避けるよう勧めている。
func (tr *transition) begin(lastFrame *ebiten.Image) {
	w, h := lastFrame.Bounds().Dx(), lastFrame.Bounds().Dy()
	if w < 1 || h < 1 {
		// 控えられないときは、進行中の転換の古い絵を新しい場面に重ねない。
		tr.on = false
		tr.release()
		return
	}
	if tr.prev != nil && (tr.prev.Bounds().Dx() != w || tr.prev.Bounds().Dy() != h) {
		tr.release()
	}
	if tr.prev == nil {
		tr.prev = ebiten.NewImage(w, h)
	}
	tr.prev.Clear()
	tr.prev.DrawImage(lastFrame, nil)
	tr.t = 0
	tr.on = true
}

// update は時間を進める。
func (tr *transition) update(dt float64) {
	if !tr.on || dt <= 0 {
		return
	}
	tr.t += dt / transitionSeconds
	if tr.t >= 1 {
		tr.t = 1
		tr.on = false
	}
}

// draw は控えてある前の画面を、残りの濃さで dst に重ねる。終わっていれば控えを捨てる。
func (tr *transition) draw(dst *ebiten.Image) {
	if !tr.on || tr.prev == nil {
		tr.release()
		return
	}
	// 転換の途中で画面の大きさが変わったら、寸法の違う絵は重ねずに打ち切る。
	if tr.prev.Bounds() != dst.Bounds() {
		tr.on = false
		tr.release()
		return
	}
	op := &ebiten.DrawImageOptions{}
	op.ColorScale.ScaleAlpha(float32(1 - tr.t))
	dst.DrawImage(tr.prev, op)
}

func (tr *transition) release() {
	if tr.prev != nil {
		tr.prev.Dispose()
		tr.prev = nil
	}
}
