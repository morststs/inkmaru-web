// Package gfx は Ebitengine と直接やり取りする描画・入力層である。
// ゲームの判断そのものは internal/ui（画面を持たなくても検証できる純粋なGo）に
// 委ね、ここは ebiten.Game の実装として値の変換と描画呼び出しに徹する。
package gfx

import (
	"image"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/morststs/inkmaru-web/internal/ai"
	"github.com/morststs/inkmaru-web/internal/netwire"
	"github.com/morststs/inkmaru-web/internal/rules"
	"github.com/morststs/inkmaru-web/internal/ui"
)

type sceneID int

const (
	sceneTitle sceneID = iota
	sceneOnline
	scenePlay
	sceneResult
)

// Game は ebiten.Game の実装。ゲームの判断は全て PlayState 側にあり、
// ここは描画と入力の変換に徹する。
type Game struct {
	cfg   rules.Config
	scene sceneID
	play  *ui.PlayState

	// net は組み立て中／対戦中のオンライン接続。CPU 対戦では nil。
	// 対戦が始まったあとも持ち続ける。PlayState.Close() が閉じるのは相手
	// （NetOpponent → Session → Transport）までで、その下の PeerConnection と
	// シグナリングの WebSocket はこの Handle からしか閉じられない。
	net *netwire.Handle
	// online は部屋の作成・参加の画面の状態。
	online onlineScene
	// peerName は相手の呼び名。CPU 対戦なら "CPU"、オンラインなら "相手"。
	// 席に依らない文言（ui.ScoreLabels・ui.ResultMessage）へ渡す。
	peerName string
	// series は同じ相手と続けた通算成績。**接続と寿命を同じにする。**
	// 再戦のあいだは持ち越し、タイトルへ戻るときと CPU 対戦を始めるときに捨てる。
	// 捨て忘れると、別の相手や CPU の結果が混ざった通算が出る。
	series ui.Series
	// recorded は今の対戦を通算に数えたか。結果画面は毎フレーム描かれるので、
	// 数える場所を描画に置くと1戦で何十回も数えることになる。
	recorded bool

	screenW, screenH int
	layout           ui.Layout
	touches          []ebiten.TouchID

	// lastPointer は Update が読んだ入力。Draw が押下の表現に使う。
	//
	// **判定を Draw でやり直してはならない。** inpututil の「今フレーム押された」
	// 系は1フレームに1回しか真にならないので、Draw で読み直すと取りこぼす。
	lastPointer Pointer

	// 場面転換のクロスフェードと、結果画面が始まってからの経過時間。
	trans   transition
	resultT float64
	// pendingTransition は、このフレームの描画のあとに転換を始めるか。
	// **Draw の中でしか画面を控えられない**ので、Update では旗だけ立てる。
	pendingTransition bool

	// frame は場面を描く先の画像で、最後に screen へ1枚転送する。
	// 転換の「前の場面の絵」は、新しい場面を描く前のこの画像から控える。
	// **screen は毎フレーム消され、描画元に使うことも避けるべきなので使わない。**
	// 画面の大きさが変わったときだけ作り直す。
	frame *ebiten.Image

	// 盤面は1ターンに1回しか変わらないため、画像をキャッシュして使い回す。
	boardImg *ebiten.Image
	pixBuf   []byte
	dirty    bool

	// 紙は固定寸法（paperSize）で1枚だけ作り、盤面の大きさへ拡大して使う。
	//
	// **盤面の寸法に合わせて作り直さない。** ブラウザのリサイズ中は Layout が
	// 毎フレーム違う値を返すので、寸法が変わるたびに作り直すと、1000px 級の盤面で
	// 100万画素を毎フレーム計算することになる。固定寸法なら作り直しは1回で済む。
	// 粒は ±3/255（paperGrain）しかなく、拡大しても目に見える模様にならない。
	paperImg *ebiten.Image

	// ドラッグの始点と、直前フレームの座標。
	// 指を離した位置はタッチAPIから取れないので lastX/lastY を使う。
	dragging       bool
	dragSX, dragSY int
	lastX, lastY   int
}

func NewGame(cfg rules.Config) *Game {
	return &Game{cfg: cfg, scene: sceneTitle, peerName: peerNameCPU, screenW: 390, screenH: 844, dirty: true}
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	if outsideWidth > 0 && outsideHeight > 0 {
		g.screenW, g.screenH = outsideWidth, outsideHeight
	}
	return g.screenW, g.screenH
}

func (g *Game) Update() error {
	g.layout = ui.ComputeLayout(g.screenW, g.screenH)
	var p Pointer
	p, g.touches = ReadPointer(g.touches)
	g.lastPointer = p

	switch g.scene {
	case sceneTitle:
		g.updateTitle(p)
	case sceneOnline:
		g.updateOnline(p)
	case scenePlay:
		g.updatePlay(p)
	default:
		g.updateResult(p)
	}

	dt := tickSeconds()
	g.trans.update(dt)
	if g.scene == sceneResult {
		g.resultT += dt
	}
	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	w, h := screen.Bounds().Dx(), screen.Bounds().Dy()
	if g.frame == nil || g.frame.Bounds().Dx() != w || g.frame.Bounds().Dy() != h {
		if g.frame != nil {
			g.frame.Dispose()
		}
		g.frame = ebiten.NewImage(w, h)
		// 作り直した直後の frame は空で、控える前の絵が無い。
		g.pendingTransition = false
	}
	if g.pendingTransition {
		// frame はまだ前フレームの絵を持っている。新しい場面を描く前に控える。
		g.trans.begin(g.frame)
		g.pendingTransition = false
	}
	g.frame.Fill(ui.ColorScreenBG)
	switch g.scene {
	case sceneTitle:
		g.drawTitle(g.frame, g.lastPointer)
	case sceneOnline:
		g.drawOnline(g.frame, g.lastPointer)
	case scenePlay:
		g.drawPlay(g.frame, g.lastPointer)
	default:
		g.drawResult(g.frame, g.lastPointer)
	}
	g.trans.draw(g.frame)
	screen.DrawImage(g.frame, nil)
}

// changeScene は場面を切り替え、前の場面からのクロスフェードを予約する。
// 結果画面に入るときは段階表示の経過時間も戻す。
func (g *Game) changeScene(s sceneID) {
	g.scene = s
	g.pendingTransition = true
	if s == sceneResult {
		g.resultT = 0
	}
}

func (g *Game) startMatch(d ai.Difficulty) {
	g.closeNet()
	g.play = ui.NewPlayState(g.cfg, d, time.Now().UnixNano())
	g.peerName = peerNameCPU
	g.series = ui.Series{}
	g.recorded = false
	g.changeScene(scenePlay)
	g.dragging = false
	g.boardImg = nil
	g.dirty = true
}

// endMatch は対戦の持ち物をすべて手放してタイトルへ戻る。
//
// **オンラインではここが接続を閉じる唯一の道である。** 第1段階には
// PlayState.Close() を呼ぶ経路が1つも無かった。CPU 相手なら何も起きないが、
// オンラインでは対戦のたびに DataChannel・PeerConnection・WebSocket と
// それぞれの goroutine が残る。ブラウザのタブは長く生きるので積み上がる。
func (g *Game) endMatch() {
	if g.play != nil {
		_ = g.play.Close()
		g.play = nil
	}
	g.closeNet()
	g.online = onlineScene{}
	g.peerName = peerNameCPU
	g.series = ui.Series{}
	g.recorded = false
	g.changeScene(sceneTitle)
	g.boardImg = nil
	g.dirty = true
}

// boardImage は盤面のキャッシュ画像を返す。dirty のときだけ作り直す。
func (g *Game) boardImage(b *rules.Board) *ebiten.Image {
	n := b.GridN()
	if g.boardImg == nil {
		g.boardImg = ebiten.NewImage(n, n)
		g.dirty = true
	}
	if g.dirty {
		g.pixBuf = ui.RenderBoardPixels(b, g.pixBuf)
		g.boardImg.WritePixels(g.pixBuf)
		g.dirty = false
	}
	return g.boardImg
}

// paperImage は紙を返す。初回に1枚だけ作る（寸法は paperSize で固定）。
func (g *Game) paperImage() *ebiten.Image {
	if g.paperImg == nil {
		g.paperImg = newPaper(paperSize, paperSize)
	}
	return g.paperImg
}

// cutBoardCorners は盤面の四隅を地の色で削る。陣地とインクの円を描き終えたあとに呼ぶ。
func (g *Game) cutBoardCorners(screen *ebiten.Image) {
	cutCorners(screen, g.layout.Board, cornerRadius, ui.ColorScreenBG)
}

// drawBoardImage は紙を敷いてから、その上に陣地のキャッシュ画像を重ねる。
//
// **陣地の画像は半透明である**（ui.RenderBoardPixels の注釈を読むこと）。
// 紙を敷かずに描くと、未取得の領域が画面の地のまま透けて見える。
func (g *Game) drawBoardImage(screen *ebiten.Image, b *rules.Board) {
	r := g.layout.Board
	// 紙の下に影を敷く。紙の四隅は描き終えたあとの cutBoardCorners が削る。
	fillRoundRect(screen, r.Add(image.Pt(0, 3)), cornerRadius, shadowColor)
	paper := g.paperImage()
	pop := &ebiten.DrawImageOptions{Filter: ebiten.FilterLinear}
	pop.GeoM.Scale(float64(r.Dx())/paperSize, float64(r.Dy())/paperSize)
	pop.GeoM.Translate(float64(r.Min.X), float64(r.Min.Y))
	screen.DrawImage(paper, pop)

	img := g.boardImage(b)
	n := float64(b.GridN())
	side := float64(r.Dx())
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Scale(side/n, side/n)
	op.GeoM.Translate(float64(r.Min.X), float64(r.Min.Y))
	screen.DrawImage(img, op)
}

// tickSeconds は Update 1回ぶんの秒数。TPS が 0 以下（ebiten.SyncWithFPS）のときは
// 時間を進めない。負の値で経過時間が戻らないようにする。
func tickSeconds() float64 {
	tps := ebiten.TPS()
	if tps <= 0 {
		return 0
	}
	return 1 / float64(tps)
}
