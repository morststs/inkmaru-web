package gfx

import (
	"fmt"
	"image"
	"math"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/morststs/inkmaru-web/internal/rules"
	"github.com/morststs/inkmaru-web/internal/ui"
)

func (g *Game) updatePlay(p Pointer) {
	wasRevealing := g.play.Phase() == ui.PhaseRevealing
	g.play.Tick(tickSeconds())
	if wasRevealing && g.play.Phase() != ui.PhaseRevealing {
		// 演出が終わったので、確定後の盤面でキャッシュを作り直す。
		g.dirty = true
	}

	// 中断は勝敗のつく終わり方ではないが、対戦の終わりではある。
	// AbortReason が入るときは PlayState が必ず PhaseFinished にしているが、
	// 理由だけが先に入る変更が入っても画面が固まらないよう、明示的に見る。
	if g.play.Phase() == ui.PhaseFinished || g.play.AbortReason() != "" {
		g.changeScene(sceneResult)
		return
	}
	if g.play.Phase() != ui.PhaseDrawing {
		return
	}

	switch {
	case p.JustPressed && ui.PointIn(g.layout.Button, p.X, p.Y):
		// 演出中はキャッシュにある「確定前の盤面」をそのまま描くため、
		// ここではキャッシュを作り直さない。演出が終わった時点で作り直す。
		_ = g.play.Confirm()

	case p.JustPressed && ui.PointIn(g.layout.Board, p.X, p.Y):
		g.dragging = true
		g.dragSX, g.dragSY = p.X, p.Y
		g.lastX, g.lastY = p.X, p.Y
		g.play.SetPreview(ui.DragCircle(g.cfg, g.layout.Board, g.dragSX, g.dragSY, p.X, p.Y))

	case p.Pressed && g.dragging:
		g.lastX, g.lastY = p.X, p.Y
		g.play.SetPreview(ui.DragCircle(g.cfg, g.layout.Board, g.dragSX, g.dragSY, p.X, p.Y))

	case p.JustReleased && g.dragging:
		// 指を離した位置はタッチAPIから取れないため、直前フレームの座標を使う。
		g.play.SetPreview(ui.DragCircle(g.cfg, g.layout.Board, g.dragSX, g.dragSY, g.lastX, g.lastY))
		g.dragging = false
	}
}

func (g *Game) drawPlay(screen *ebiten.Image, p Pointer) {
	// 演出中は「確定前の盤面」を下地にし、その上で円を広げて見せる。
	board := g.play.Match().Board()
	if g.play.Phase() == ui.PhaseRevealing {
		board = g.play.BoardBefore()
	}
	g.drawBoardImage(screen, board)

	switch g.play.Phase() {
	case ui.PhaseRevealing:
		// インクが落ちて広がる見え方にする。ui 側の進行（線形）は変えず、
		// ここで曲線をかけるだけに留める。
		t := g.play.RevealProgress()
		ease := 1 - math.Pow(1-t, 3)
		moves := g.play.LastMoves()
		for i, pl := range []rules.Player{rules.P1, rules.P2} {
			g.drawInkCircle(screen, moves[i], pl, ease)
		}
	case ui.PhaseDrawing:
		if c, ok := g.play.Preview(); ok {
			// **自分の色は席から引く。** P1 決め打ちにすると、ゲスト（P2）の
			// 画面では自分の円が相手の色で描かれる。
			seat := g.play.Seat()
			g.drawInkCircle(screen, c, seat, 1)
		}
	}

	g.cutBoardCorners(screen)

	g.drawPlayHeader(screen)
	g.drawScoreBar(screen)
	g.drawPlayInfo(screen)
	g.drawConfirmButton(screen, p)
}

// drawInkCircle は円を、その濃さに応じた不透明度で、にじませて描く。
// scale は演出用に半径を縮める係数（0 から 1 へ動かすと広がって見える）。
// 盤面の外にはみ出さないよう SubImage に描く。
func (g *Game) drawInkCircle(screen *ebiten.Image, c rules.Circle, p rules.Player, scale float64) {
	cx, cy := ui.BoardToScreen(g.layout.Board, c.CX, c.CY)
	r := ui.RadiusToScreen(g.layout.Board, c.R) * float32(scale)
	base := ui.PlayerColor(p)
	a := ui.AlphaForDensity(g.cfg, rules.Density(g.cfg.InkPerTurn, c.R))
	dst := screen.SubImage(g.layout.Board).(*ebiten.Image)
	fillInkCircle(dst, cx, cy, r, base, a)
	strokeInkRim(dst, cx, cy, r, base, a)
}

func (g *Game) drawPlayHeader(screen *ebiten.Image) {
	turn := g.play.Match().Turn() + 1
	if turn > g.cfg.Turns {
		turn = g.cfg.Turns
	}
	msg := fmt.Sprintf("ターン %d / %d", turn, g.cfg.Turns)
	// 制限時間があるのはオンライン対戦だけである。CPU 相手では ok が偽になり、
	// 第1段階のヘッダと1文字も変わらない。
	// 数えるのは描いている間だけ。相手待ちの最中も出すと、自分はもう出したのに
	// 0秒へ落ちていく数字を見せることになる。
	if d, ok := g.play.Remaining(); ok && g.play.Phase() == ui.PhaseDrawing {
		msg += fmt.Sprintf("   のこり %d秒", int(math.Ceil(d.Seconds())))
	}
	x, y := centreOf(g.layout.Header)
	drawText(screen, msg, x, y, 2, ui.ColorText)
}

// drawScoreBar は陣地の割合を帯グラフで描く。
// 描いている最中は「いま確定したらどうなるか」を示すため、
// 大きさを変えながら結果を確かめられる。
func (g *Game) drawScoreBar(screen *ebiten.Image) {
	r := g.layout.ScoreBar
	s := g.play.PreviewScore()

	barTop, barH := r.Min.Y, 10
	bar := image.Rect(r.Min.X, barTop, r.Max.X, barTop+barH)
	// 下地。区画の切り捨てでできる 1px の継ぎ目を埋める。
	fillRoundRect(screen, bar, barRadius, ui.ColorButtonDisabled)
	x := float64(r.Min.X)
	w := float64(r.Dx())
	// 並びは盤面の順（P1・中立・P2）で固定し、見出しだけを席から決める。
	// 席で並べ替えると、同じ盤面が2つの画面で違って見える。
	labels := ui.ScoreLabels(g.play.Seat(), g.peerName)
	parts := make([]string, 0, len(labels))
	for _, seg := range labels {
		dw := w * s.Share(seg.Player)
		clip := image.Rect(int(x), barTop, int(x+dw), barTop+barH)
		if !clip.Empty() {
			// 区画ごとに「帯全体の角丸」を描き、SubImage でその区画の幅だけに
			// 切り取る。こうすると両端だけが丸まり、区画の境目は直角のまま残る。
			fillRoundRect(screen.SubImage(clip).(*ebiten.Image), bar, barRadius, ui.PlayerColor(seg.Player))
		}
		x += dw
		parts = append(parts, fmt.Sprintf("%s %.0f%%", seg.Label, 100*s.Share(seg.Player)))
	}

	drawText(screen, strings.Join(parts, "   "),
		float64(r.Min.X)+w/2, float64(barTop+barH)+12, 1, ui.ColorText)
}

func (g *Game) drawPlayInfo(screen *ebiten.Image) {
	msg := "盤面をドラッグして円を描く"
	if g.play.Phase() == ui.PhaseWaitingPeer {
		// CPU は即答するのでこの状態は観測されない。出るのはオンラインだけである。
		msg = g.peerName + "が考えています"
	} else if c, ok := g.play.Preview(); ok {
		area := 100 * math.Pi * c.R * c.R
		msg = fmt.Sprintf("大きさ %.0f%%   濃さ %.1f",
			area, rules.Density(g.cfg.InkPerTurn, c.R))
	}
	x, y := centreOf(g.layout.Info)
	drawText(screen, msg, x, y, 1, ui.ColorText)
}

func (g *Game) drawConfirmButton(screen *ebiten.Image, p Pointer) {
	r := g.layout.Button
	_, ready := g.play.Preview()
	em := ui.EmphasisDisabled
	if ready && g.play.Phase() == ui.PhaseDrawing {
		em = ui.EmphasisPrimary
	}
	drawButton(screen, r, "決定する", em, p.Pressed && ui.PointIn(r, p.X, p.Y))
}
