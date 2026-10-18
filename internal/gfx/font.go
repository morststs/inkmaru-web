package gfx

import (
	"bytes"
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/morststs/inkmaru-web/internal/assets"
)

// baseFontSize は drawText の scale=1 に対応する文字の大きさ（px）。
// 12px は、以前使っていたビットマップフォントの大きさでもある。画面の桁数を
// 数える ui.InfoCols が「全角は12px」を前提にしているので、ここを変えるなら
// あちらも一緒に変えること。
const baseFontSize = 12

var (
	// 字形の本体。M PLUS Rounded 1c Medium を、このゲームが出す文字だけに
	// 絞り込んだもの（internal/assets）。
	//
	// **控えのフォントは持たない。** 以前はビットマップフォントを
	// text.NewMultiFace で後ろに置き、絞り込みから漏れた字の受け皿にしていたが、
	// それだけで配信物が 506 KB（Brotli 後）増えた。絞り込みの漏れは
	// internal/assets のテスト2本がビルド時に落として防ぐので、毎回の
	// ダウンロードで払う保険としては高すぎるという判断である
	// （2026-10-10 実測。CLAUDE.md「画面のフォント」に経緯がある）。
	// 漏らした字は □ になる。
	gameFaceSource = mustFaceSource(assets.MPlusRounded1cMedium)

	// 大きさごとの字面。Ebitengine は字形の画像を大きさごとに作るため、
	// 毎フレーム作り直すと描画のたびに作り直しになる。
	faceCache = map[float64]text.Face{}
)

func mustFaceSource(b []byte) *text.GoTextFaceSource {
	src, err := text.NewGoTextFaceSource(bytes.NewReader(b))
	if err != nil {
		// 焼き込んだ素材が読めないのはビルドの破損であって、実行時の事情では
		// ない。同じバイト列を同じライブラリで読む検査が internal/assets に
		// あるので、ここに来るならテストも落ちている。
		panic("gfx: 埋め込んだフォントが読めない: " + err.Error())
	}
	return src
}

// faceFor は scale 倍の大きさの字面を返す。
func faceFor(scale float64) text.Face {
	if f, ok := faceCache[scale]; ok {
		return f
	}
	f := &text.GoTextFace{Source: gameFaceSource, Size: baseFontSize * scale}
	faceCache[scale] = f
	return f
}

// drawText は (x, y) を中心として文字列を描く。
// PrimaryAlign / SecondaryAlign を中央にしているため、GeoM の平行移動先が
// そのまま文字の中心になる。
func drawText(dst *ebiten.Image, s string, x, y, scale float64, clr color.Color) {
	op := &text.DrawOptions{}
	op.GeoM.Translate(x, y)
	op.ColorScale.ScaleWithColor(clr)
	op.PrimaryAlign = text.AlignCenter
	op.SecondaryAlign = text.AlignCenter
	text.Draw(dst, s, faceFor(scale), op)
}

// centreOf は矩形の中心座標を返す。文字を置く位置の指定に使う。
func centreOf(r image.Rectangle) (float64, float64) {
	return float64(r.Min.X) + float64(r.Dx())/2, float64(r.Min.Y) + float64(r.Dy())/2
}
