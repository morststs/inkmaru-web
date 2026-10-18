package gfx

import (
	"fmt"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/morststs/inkmaru-web/internal/netplay"
	"github.com/morststs/inkmaru-web/internal/netwire"
	"github.com/morststs/inkmaru-web/internal/signaling"
	"github.com/morststs/inkmaru-web/internal/ui"
)

// signalingURL は配備済みのシグナリングサーバ（第2段階a）。
//
// **接続先を書く場所はここ1箇所だけである。** 複数箇所に散ると、片方だけ
// 直した版が「繋がらない」ではなく「自分の部屋に誰も来ない」という
// 気づきにくい形で壊れる（作る側と入る側が別のサーバを見る）。
const signalingURL = "wss://zlmj70071m.execute-api.ap-northeast-1.amazonaws.com/prod"

// 相手の呼び名。席に依らない文言（ui.ScoreLabels・ui.ResultMessage）へ渡す。
const (
	peerNameCPU    = "CPU"
	peerNameOnline = "相手"
)

// 情報欄に折り返して描くときの行の高さと行数の上限。
const (
	infoLineH    = 14
	infoMaxLines = 3
)

// onlineScene は部屋の作成・参加の画面が持つ状態。
// 接続そのものは Game.net が持つ（対戦が始まってからも生き続けるため）。
type onlineScene struct {
	entry ui.RoomCodeEntry
	// notice はこちらの都合で出す一言（番号が足りない等）。
	notice string
}

func (g *Game) updateOnline(p Pointer) {
	if g.net != nil {
		// **毎フレーム進める。** Update はブロックしない。
		if g.net.Update() == netplay.StateReady {
			g.startOnlineMatch()
			return
		}
	}
	if !p.JustPressed {
		return
	}

	// 添字ではなく意味（b.Action）で分岐する。添字の解釈が押下と描画に
	// 分かれていると、片方だけ並べ替えたときに「もどる」で部屋が作られる。
	for _, b := range ui.OnlineButtons(g.layout, g.net != nil) {
		if !ui.PointIn(b.Rect, p.X, p.Y) {
			continue
		}
		// 描画と同じ Emphasis で弾く。押せないボタンは反応しない。
		if b.Emphasis == ui.EmphasisDisabled {
			return
		}
		g.pressOnlineButton(b.Action)
		return
	}
	if g.net != nil {
		// 組み立てを始めたあとはキーパッドを受け付けない。
		return
	}
	for _, k := range ui.KeypadKeys(g.layout) {
		if !ui.PointIn(k.Rect, p.X, p.Y) {
			continue
		}
		if k.Rune == 0 {
			g.online.entry.Backspace()
		} else {
			g.online.entry.Append(k.Rune)
		}
		g.online.notice = ""
		return
	}
}

func (g *Game) pressOnlineButton(a ui.OnlineAction) {
	if a == ui.OnlineBack {
		g.leaveOnline()
		return
	}
	// 組み立て中に [もどる] 以外を弾くのは ui.OnlineButtons の Emphasis で済んでいる。
	switch a {
	case ui.OnlineCreate:
		g.online.notice = ""
		g.net = netwire.Start(signalingURL, true, "")
	case ui.OnlineJoin:
		if !g.online.entry.Complete() {
			g.online.notice = fmt.Sprintf("部屋番号を%d文字入れてください", signaling.CodeLength)
			return
		}
		g.online.notice = ""
		g.net = netwire.Start(signalingURL, false, g.online.entry.Code())
	}
}

// startOnlineMatch は開通した接続で対戦を始める。
//
// **構築子は NewPlayStateWithSeat である。** 席を渡さない版を使うと両者が
// P1 になり、盤面が互いの鏡像になって全対戦が1ターン目で board-mismatch に
// なる（第2段階で最も高くついた欠陥）。
//
// g.net はここでは閉じない。接続は対戦が終わるまで要る（endMatch が閉じる）。
// 制限時間の起点はここでは立てない。netplay.Connector.pollTransport が
// StateReady へ移す直前に既に NetOpponent.Start() を呼んでおり
// （connect.go、「Ready の瞬間に Start を呼ぶこと」を固定した既存テストがある）、
// NetOpponent.markStart は冪等なので、ここで呼び直しても何も起きない。
// 「ここが起点である」という注釈を残すと、根拠の無い事実が1つ増える。
//
// **対戦中はシグナリングの通知を見ない。これは意図した設計である。**
// netwire.Handle.Update() を呼ぶのは updateOnline の1箇所だけなので、
// scenePlay に入った時点でシグナリング経由の peer-left は観測しなくなる。
// 仕様書 §7.2 は「peer-left をシグナリング経由で受け取るか、DataChannel が
// 閉じたら」と選択で書いており、対戦中の切断は DataChannel の OnClose と
// PeerConnection の状態遷移（internal/rtc/peer.go が netplay.ErrPeerGone に
// 翻訳する）、および NetOpponent の相手待ちの打ち切りが覆っている。
// 対戦中も Update を呼ぶことにすると、開通後の Connector が毎フレーム
// signal を引いて handle し続けることになる（Update は StateReady でも
// flushLocalCandidates と sig.Next の処理を続ける）。覆いは既に冗長なので、
// そこまでして検出の経路を1本増やす理由が無い。
// 読まれない通知は sigclient の受信 goroutine が msgs（recvBuffer = 64）へ
// 溜めるが、溢れてもその goroutine が待つだけで取りこぼしは起きず
// （sigclient の recvBuffer の注釈）、対戦が終われば endMatch が接続ごと閉じる。
func (g *Game) startOnlineMatch() {
	opp := g.net.Opponent()
	g.play = ui.NewPlayStateWithSeat(g.cfg, opp, g.net.Seat())
	g.peerName = peerNameOnline
	g.series = ui.Series{}
	g.recorded = false
	g.online = onlineScene{}
	g.changeScene(scenePlay)
	g.dragging = false
	g.boardImg = nil
	g.dirty = true
}

// leaveOnline はオンライン画面から抜けてタイトルへ戻る。
//
// **netwire.Handle に取り消しの API は無く、Close() を呼んでも State() は
// StateWaitingPeer のままである。** だから「取り消した」ことは画面側が
// 持ち物を捨てることで表す。捨て忘れると、タイトルに戻ったあとも
// WebSocket と受信 goroutine が残り続ける。
func (g *Game) leaveOnline() {
	g.closeNet()
	g.online = onlineScene{}
	g.changeScene(sceneTitle)
}

func (g *Game) closeNet() {
	if g.net != nil {
		g.net.Close()
		g.net = nil
	}
}

func (g *Game) drawOnline(screen *ebiten.Image, p Pointer) {
	x, y := centreOf(g.layout.Header)
	drawText(screen, "オンライン対戦", x, y, 2, ui.ColorText)

	if g.net == nil {
		g.drawOnlineEntry(screen, p)
	} else {
		g.drawOnlineProgress(screen)
	}
	g.drawOnlineButtons(screen, p)
}

// drawOnlineEntry は部屋番号の入力欄とキーパッドを描く。
func (g *Game) drawOnlineEntry(screen *ebiten.Image, p Pointer) {
	x, y := centreOf(g.layout.ScoreBar)
	drawText(screen, g.online.entry.Display(), x, y, 3, ui.ColorText)

	for _, k := range ui.KeypadKeys(g.layout) {
		kx, ky := centreOf(k.Rect)
		label := "←" // Rune が 0 のキーは1文字消す取り消し
		if k.Rune != 0 {
			label = string(k.Rune)
		}
		// キーは32個あるので影は敷かない（毎フレームのパス描画が倍になる）。
		bg := ui.ColorButtonTitle
		if p.Pressed && ui.PointIn(k.Rect, p.X, p.Y) {
			bg = darken(bg, 0.15)
		}
		fillRoundRect(screen, k.Rect, keyRadius, bg)
		drawText(screen, label, kx, ky, 2, ui.ColorText)
	}

	msg := g.online.notice
	if msg == "" {
		msg = "部屋を作るか、相手の番号を入れる"
	}
	g.drawInfoLines(screen, []string{msg})
}

// drawOnlineProgress は接続の組み立て中の様子を描く。
// 状態と文言の対応は ui.ConnectStatus が持つ（そちらでテストしてある）。
func (g *Game) drawOnlineProgress(screen *ebiten.Image) {
	head, detail := ui.ConnectStatus(g.net.Phase(), g.net.Room(), g.net.FailReason(),
		ui.InfoCols(g.layout), infoMaxLines)
	x, y := centreOf(g.layout.ScoreBar)
	drawText(screen, head, x, y, 3, ui.ColorText)
	g.drawInfoLines(screen, detail)
}

func (g *Game) drawOnlineButtons(screen *ebiten.Image, p Pointer) {
	for _, b := range ui.OnlineButtons(g.layout, g.net != nil) {
		drawButton(screen, b.Rect, b.Label, b.Emphasis,
			p.Pressed && ui.PointIn(b.Rect, p.X, p.Y))
	}
}

// drawInfoLines は情報欄に複数行を上下中央で描く。
func (g *Game) drawInfoLines(screen *ebiten.Image, lines []string) {
	if len(lines) == 0 {
		return
	}
	r := g.layout.Info
	x := float64(r.Min.X) + float64(r.Dx())/2
	top := float64(r.Min.Y) + float64(r.Dy())/2 - float64(len(lines)-1)*infoLineH/2
	for i, line := range lines {
		drawText(screen, line, x, top+float64(i)*infoLineH, 1, ui.ColorText)
	}
}
