package gfx

import (
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/morststs/inkmaru-web/internal/ui"
)

// fillInkCircle は、にじみのある円を描く。
// a は中心部の合成不透明度（0..1）。層の半径と不透明度は ui.InkBleedRadii と
// ui.InkLayerAlphas が決める（合成が a を超えないことはそちらのテストが固定している）。
func fillInkCircle(dst *ebiten.Image, cx, cy, r float32, base color.NRGBA, a float64) {
	if a <= 0 {
		return
	}
	for i, av := range ui.InkLayerAlphas(a) {
		if av <= 0 {
			continue
		}
		clr := color.NRGBA{R: base.R, G: base.G, B: base.B, A: uint8(av*255 + 0.5)}
		vector.FillCircle(dst, cx, cy, r*float32(ui.InkBleedRadii[i]), clr, true)
	}
}

// strokeInkRim は円の縁に細い線を引く。インクが縁に溜まった見え方になる。
func strokeInkRim(dst *ebiten.Image, cx, cy, r float32, base color.NRGBA, a float64) {
	if a <= 0 {
		return
	}
	av := a * 0.85
	if av > 1 {
		av = 1
	}
	clr := color.NRGBA{R: base.R, G: base.G, B: base.B, A: uint8(av*255 + 0.5)}
	vector.StrokeCircle(dst, cx, cy, r, 1.5, clr, true)
}
