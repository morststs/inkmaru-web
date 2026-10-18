package gfx

import (
	"fmt"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/morststs/inkmaru-web/internal/rules"
	"github.com/morststs/inkmaru-web/internal/ui"
)

func (g *Game) updateResult(p Pointer) {
	// 通算は1戦につき1回だけ数える。結果画面は毎フレーム描かれるので、
	// 数える場所を描画側に置くと1戦で何十回も数えることになる。
	// 中断で終わった対戦（Result が決着していない）は ui.Series が無視する。
	if !g.recorded {
		g.series.Record(g.play.Match().Result(), g.play.Seat())
		g.recorded = true
	}

	// 申し出たあとは相手の返事を待つ。**毎フレーム引く必要がある。**
	// ここで引かないと、相手が「もう一度」を押しても届いたことに気づかず、
	// 60秒待って「戻りませんでした」になる。
	if g.play.RematchAsked() {
		// エラーは PlayState が「相手は戻りませんでした」として抱える。
		// ここでタイトルへ飛ばすと、その一言を読む間が無い。
		agreed, _ := g.play.PollRematch()
		if agreed {
			g.startRematch()
			return
		}
	}

	if !p.JustPressed {
		return
	}
	// 添字ではなく意味（b.Action）で分岐する。
	for _, b := range ui.ResultButtons(g.layout, g.play.CanRematch(), g.play.RematchAsked()) {
		if !ui.PointIn(b.Rect, p.X, p.Y) {
			continue
		}
		if b.Action == ui.ResultRematch {
			if err := g.play.RequestRematch(); err != nil {
				g.endMatch()
			}
			return
		}
		// 対戦の持ち物（オンラインでは接続一式）をここで手放す。
		g.endMatch()
		return
	}
}

// startRematch は同じ相手と次の対戦を始める。
//
// **PlayState.Close() を呼んではならない。** 呼ぶと DataChannel ごと閉じて
// 相手が居なくなる（再戦の設計 §8）。相手役（NetOpponent）は使い回す。
// 通算成績はここでは捨てない。捨てるのはタイトルへ戻るときである。
func (g *Game) startRematch() {
	g.play = ui.NewPlayStateWithSeat(g.cfg, g.net.Opponent(), g.net.Seat())
	g.recorded = false
	g.changeScene(scenePlay)
	g.dragging = false
	g.boardImg = nil
	g.dirty = true
}

func (g *Game) drawResult(screen *ebiten.Image, p Pointer) {
	g.drawBoardImage(screen, g.play.Match().Board())
	g.cutBoardCorners(screen)
	// 盤面 → 数字 → 勝敗の順に出す。経過時間は Update が足している（一時停止から
	// 戻っても段階は巻き戻らない。ui.ResultStageAt の注釈を読むこと）。
	// **StageBar に対応する帯グラフは結果画面に無い**ので、その段階は盤面だけが
	// 見える時間として使う。
	stage := ui.ResultStageAt(time.Duration(g.resultT * float64(time.Second)))
	s := g.play.Match().Board().Score()
	seat := g.play.Seat()

	// **勝敗の文言は席から決める。** WinP1 を「あなたの勝ち」と決め打ちにすると、
	// ゲスト（席=P2）は勝った対戦で「相手の勝ち」を見ることになる。
	// 相手が離れたときに自分の勝ちにする規則
	// （仕様書 §5.4）も ui.ResultMessage が持つ。ゲーム規則であって
	// 描画ではないので、テストの書けるところに置いてある。
	if stage >= ui.StageMessage {
		msg := ui.ResultMessage(g.play.Match().Result(), seat, g.peerName, g.play.PeerLeft())
		x, y := centreOf(g.layout.Header)
		drawText(screen, msg, x, y, 3, ui.ColorText)
	}

	// 見出しと割合の対応づけは ui.ResultShares が持つ。ここで手組みすると、
	// 2つの Share() を入れ替えてもどのテストも落ちない（ゲストの画面だけで
	// 自分と相手の数字が入れ替わる、第2段階で最も高くついた欠陥と同じ形）。
	if stage >= ui.StageNumbers {
		parts := make([]string, 0, 2)
		for _, sl := range ui.ResultShares(s, seat, g.peerName) {
			parts = append(parts, fmt.Sprintf("%s %.1f%%", sl.Label, 100*s.Share(sl.Player)))
		}
		x, y := centreOf(g.layout.ScoreBar)
		drawText(screen, strings.Join(parts, "   "), x, y, 2, ui.ColorText)
	}

	g.drawResultInfo(screen, s)
	g.drawResultButtons(screen, p)
}

// drawResultInfo は情報欄に、通算成績と（待っていれば）相手の返事の残り時間、
// そうでなければ対戦の詳細を出す。
//
// 何を出すかを決めているのはここだが、**中身の判断は全て ui 側にある**。
// 通算の一言は2戦目から出る（1戦目は ui.Series.Line が空文字を返す）。
func (g *Game) drawResultInfo(screen *ebiten.Image, s rules.Score) {
	lines := make([]string, 0, infoMaxLines)
	if line := g.series.Line(); line != "" {
		lines = append(lines, line)
	}
	if status := g.play.RematchStatus(); len(status) > 0 {
		lines = append(lines, status...)
	} else {
		lines = append(lines, ui.ResultDetail(s, g.peerName, g.play.AbortReason(),
			g.play.PeerLeft(), ui.InfoCols(g.layout), infoMaxLines-len(lines))...)
	}
	g.drawInfoLines(screen, lines)
}

func (g *Game) drawResultButtons(screen *ebiten.Image, p Pointer) {
	for _, b := range ui.ResultButtons(g.layout, g.play.CanRematch(), g.play.RematchAsked()) {
		drawButton(screen, b.Rect, b.Label, b.Emphasis,
			p.Pressed && ui.PointIn(b.Rect, p.X, p.Y))
	}
}
