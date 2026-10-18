package gfx

import (
	"image"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/morststs/inkmaru-web/internal/ai"
	"github.com/morststs/inkmaru-web/internal/rules"
	"github.com/morststs/inkmaru-web/internal/ui"
)

var titleDifficulties = [3]ai.Difficulty{ai.Easy, ai.Normal, ai.Hard}

func (g *Game) updateTitle(p Pointer) {
	if !p.JustPressed {
		return
	}
	if ui.PointIn(ui.TitleOnlineButton(g.layout), p.X, p.Y) {
		g.online = onlineScene{}
		g.changeScene(sceneOnline)
		return
	}
	for i, r := range ui.TitleButtons(g.layout) {
		if ui.PointIn(r, p.X, p.Y) {
			g.startMatch(titleDifficulties[i])
			return
		}
	}
}

// titleBlots はタイトル画面の地に散らす墨の滲み。
// 画面の短辺に対する比で持つので、どの画面サイズでも同じ構図になる。
// **固定の値である。** 乱数にすると、起動するたび構図が変わって落ち着かない。
//
// **上の帯にだけ置く。** 中央は難易度ボタンが覆って見えず、下の帯は
// drawRuleSketch の場所である。仕組みの絵と同じ見た目の円を隣に置くと、
// どれが説明でどれが飾りか見分けられなくなる（2026-10-11、画面を見て判断）。
var titleBlots = [...]struct {
	x, y, r float64
	p       rules.Player
}{
	{0.18, 0.16, 0.16, rules.P1},
	{0.82, 0.20, 0.10, rules.P2},
}

// drawTitleBlots は地に薄い墨の滲みを描く。文字の読みやすさを損なわない
// 濃さに留める（ColorText との比を落とさない）。
func (g *Game) drawTitleBlots(screen *ebiten.Image) {
	w := float64(g.layout.Screen.Dx())
	h := float64(g.layout.Screen.Dy())
	short := w
	if h < short {
		short = h
	}
	for _, b := range titleBlots {
		fillInkCircle(screen,
			float32(b.x*w), float32(b.y*h), float32(b.r*short),
			ui.PlayerColor(b.p), 0.10)
	}
}

// drawRuleSketch は「小さい円ほど濃く、重なりで勝つ」を絵で見せる。
// 薄い大円と濃い小円を重ね、重なった部分が濃いほうの色になることを示す。
//
// **文字の説明は残す。** 絵だけでは「濃いほうが勝つ」とまでは読めない。
func (g *Game) drawRuleSketch(screen *ebiten.Image, r image.Rectangle) {
	if r.Dx() < 40 || r.Dy() < 40 {
		return // 潰れた画面では描かない
	}
	cx := float32(r.Min.X + r.Dx()/2)
	cy := float32(r.Min.Y + r.Dy()/2)
	// 絵の広がりは、にじみの最外層（×1.06）を含めて cx から見て
	// 右が +1.251*unit（大円の中心 +0.35 と半径 0.85*1.06）、
	// 左が -0.827*unit（小円の中心 -0.35 と半径 0.45*1.06）で、右に約 0.21*unit 寄る。
	// 中心を 0.2*unit 左へずらして揃える（下の cx）。揃えたあとの半幅は約 1.04*unit。
	// 幅が狭い隙間でも左右に切れないよう、幅からも unit を抑える（1.3 で割る。安全側）。
	unit := float32(r.Dy()) / 2
	if u := float32(r.Dx()) / 1.3 / 2; u < unit {
		unit = u
	}

	cx -= unit * 0.2
	// 薄い大円（朱）
	fillInkCircle(screen, cx+unit*0.35, cy, unit*0.85, ui.ColorP2, ui.AlphaLo+0.06)
	// 濃い小円（藍）
	fillInkCircle(screen, cx-unit*0.35, cy, unit*0.45, ui.ColorP1, ui.AlphaHi)
}

func (g *Game) drawTitle(screen *ebiten.Image, p Pointer) {
	g.drawTitleBlots(screen)

	x, y := centreOf(g.layout.Header)
	drawText(screen, "Inkmaru", x, y, 3, ui.ColorText)

	x, y = centreOf(g.layout.ScoreBar)
	drawText(screen, "円を描いて陣地を取り合う", x, y, 1, ui.ColorText)

	btns := ui.TitleButtons(g.layout)
	for i, r := range btns {
		drawButton(screen, r, "CPU対戦 ・ "+titleDifficulties[i].String(),
			ui.EmphasisSecondary, p.Pressed && ui.PointIn(r, p.X, p.Y))
	}

	// 最後のボタンと情報欄の隙間に、仕組みの絵を置く。
	gapTop := btns[len(btns)-1].Max.Y
	gapBottom := g.layout.Info.Min.Y
	if gapBottom > gapTop {
		g.drawRuleSketch(screen, image.Rect(
			g.layout.Info.Min.X, gapTop, g.layout.Info.Max.X, gapBottom))
	}

	x, y = centreOf(g.layout.Info)
	drawText(screen, "小さい円ほど濃く、重なりで勝つ", x, y, 1, ui.ColorText)

	r := ui.TitleOnlineButton(g.layout)
	drawButton(screen, r, "オンライン対戦", ui.EmphasisPrimary,
		p.Pressed && ui.PointIn(r, p.X, p.Y))
}
