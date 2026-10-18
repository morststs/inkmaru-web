package gfx

import (
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
	"github.com/morststs/inkmaru-web/internal/ui"
)

// cornerRadius はボタンや盤面の角の丸み（px）。
const cornerRadius = 6

// keyRadius はキーパッドのキーの角の丸み。キーは1辺が40px前後と小さく、
// 6px だと丸が目立ちすぎるので、ボタンより小さくしてある。
const keyRadius = 4

// barRadius はスコア帯の角の丸み。帯の高さが10pxしかないので、半径は
// 高さの半分（5px）より小さくする。
const barRadius = 3

// roundRectPath は角丸矩形のパスを p に組み立てる。
//
// 半径が矩形の半分を超えると角が交差して破綻するので、そこで丸める。
// 狭い画面ではボタンの高さが数 px まで潰れることがある。
func roundRectPath(p *vector.Path, r image.Rectangle, radius float32) {
	x0, y0 := float32(r.Min.X), float32(r.Min.Y)
	x1, y1 := float32(r.Max.X), float32(r.Max.Y)
	if h := (y1 - y0) / 2; radius > h {
		radius = h
	}
	if w := (x1 - x0) / 2; radius > w {
		radius = w
	}
	if radius < 0 {
		radius = 0
	}
	p.MoveTo(x0+radius, y0)
	p.LineTo(x1-radius, y0)
	p.ArcTo(x1, y0, x1, y0+radius, radius)
	p.LineTo(x1, y1-radius)
	p.ArcTo(x1, y1, x1-radius, y1, radius)
	p.LineTo(x0+radius, y1)
	p.ArcTo(x0, y1, x0, y1-radius, radius)
	p.LineTo(x0, y0+radius)
	p.ArcTo(x0, y0, x0+radius, y0, radius)
	p.Close()
}

// fillRoundRect は角丸矩形を塗る。
func fillRoundRect(dst *ebiten.Image, r image.Rectangle, radius float32, clr color.Color) {
	if r.Empty() {
		return
	}
	var p vector.Path
	roundRectPath(&p, r, radius)
	op := &vector.DrawPathOptions{AntiAlias: true}
	op.ColorScale.ScaleWithColor(clr)
	vector.FillPath(dst, &p, nil, op)
}

// strokeRoundRect は角丸矩形の枠線を引く。
func strokeRoundRect(dst *ebiten.Image, r image.Rectangle, radius, width float32, clr color.Color) {
	if r.Empty() {
		return
	}
	var p vector.Path
	roundRectPath(&p, r, radius)
	op := &vector.DrawPathOptions{AntiAlias: true}
	op.ColorScale.ScaleWithColor(clr)
	vector.StrokePath(dst, &p, &vector.StrokeOptions{Width: width}, op)
}

// cutCorners は矩形の四隅を、角丸の外側だけ clr で塗りつぶして削る。
//
// 紙の角は透明にしてあるが、その上に重ねる陣地やインクの円は矩形全体を
// 覆うので、角に触れたところで四角が戻ってしまう。描き終えたあとにこれで削る。
// 地が単色の画面でしか使えない。
func cutCorners(dst *ebiten.Image, r image.Rectangle, radius float32, clr color.Color) {
	if r.Empty() {
		return
	}
	var p vector.Path
	// 外側の矩形。内側の角丸矩形との間を偶奇規則で抜き、角だけが残る。
	x0, y0 := float32(r.Min.X), float32(r.Min.Y)
	x1, y1 := float32(r.Max.X), float32(r.Max.Y)
	p.MoveTo(x0, y0)
	p.LineTo(x1, y0)
	p.LineTo(x1, y1)
	p.LineTo(x0, y1)
	p.Close()
	roundRectPath(&p, r, radius)
	op := &vector.DrawPathOptions{AntiAlias: true}
	op.ColorScale.ScaleWithColor(clr)
	vector.FillPath(dst, &p, &vector.FillOptions{FillRule: vector.FillRuleEvenOdd}, op)
}

// shadowColor は要素の下に敷く影。地より暗くして浮いて見せる。
var shadowColor = color.NRGBA{R: 0x0E, G: 0x0D, B: 0x0C, A: 0xB0}

// drawButton はボタン1つを、強調度と押下状態に応じて描く。
//
// **押下中は1px 沈める。** 押したことが画面に出ないと、反応が無いと思って
// 連打される（接続の組み立ては 1.4 秒かかることがある）。
func drawButton(dst *ebiten.Image, r image.Rectangle, label string, em ui.Emphasis, pressed bool) {
	if r.Empty() {
		return
	}
	// 押せないボタンは沈めない。沈むと「押したことを受け付けた」と言うことになる。
	pressed = pressed && em != ui.EmphasisDisabled
	body := r
	if pressed {
		body = body.Add(image.Pt(0, 1))
	} else {
		fillRoundRect(dst, r.Add(image.Pt(0, 2)), cornerRadius, shadowColor)
	}

	fg := ui.ColorText
	switch em {
	case ui.EmphasisPrimary:
		bg := ui.ColorButtonPrimary
		if pressed {
			bg = darken(bg, 0.15)
		}
		fillRoundRect(dst, body, cornerRadius, bg)
	case ui.EmphasisSecondary:
		bg := ui.ColorButtonTitle
		if pressed {
			bg = darken(bg, 0.15)
		}
		fillRoundRect(dst, body, cornerRadius, bg)
		strokeRoundRect(dst, body, cornerRadius, 1, ui.ColorTextDisabled)
	default:
		// 押せないボタンの見た目。**未知の強調度もここで受ける。** ui に強調度を
		// 足して gfx の追従を忘れたとき、ボタンが黙って消えるより、灰色で
		// 出るほうが気づける。
		fillRoundRect(dst, body, cornerRadius, ui.ColorButtonDisabled)
		fg = ui.ColorTextDisabled
	}

	x, y := centreOf(body)
	drawText(dst, label, x, y, fitScale(label, r.Dx()), fg)
}

// fitScale は、ボタンの幅に収まる文字の大きさを返す。
// 大きいほうから順に試し、入る最初のものを使う。
//
// **2 と 1 の間を飛ばすと落差が目立つ。** ボタンが3つ横に並ぶオンライン画面で
// 「部屋を作る」だけが 1 に落ちると、主たる操作の文字がいちばん小さくなる
// （2026-10-11 実測）。文言は変えられないので、こちらを刻む。
func fitScale(label string, w int) float64 {
	const pad = 12 // 左右に残す余白の合計
	for _, s := range []float64{2, 1.5, 1} {
		if text.Advance(label, faceFor(s)) <= float64(w-pad) {
			return s
		}
	}
	return 1
}

// darken は色を暗くする。押下の表現に使う。
func darken(c color.NRGBA, f float64) color.NRGBA {
	return color.NRGBA{
		R: byte(float64(c.R) * (1 - f)),
		G: byte(float64(c.G) * (1 - f)),
		B: byte(float64(c.B) * (1 - f)),
		A: c.A,
	}
}
