package gfx

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/morststs/inkmaru-web/internal/ui"
)

// 紙の地合いの振幅（0〜255 の明暗）。
//
// **±3 を超えさせない。** 紙の粒は陣地の下に見えるので、表示色を揺らす。
// 揺らしが大きすぎると、離れた2点の濃淡を取り違える——このゲームでは
// 「濃い＝強い」が見た目とルールの一致そのものなので、嘘をつくことになる
// （仕様書 §4.4）。
const paperGrain = 3

// paperSeed は地合いの乱数の種。固定してあるので、同じ寸法なら毎回同じ紙になる。
// 毎フレーム違う紙にすると、止まっている画面がちらつく。
const paperSeed = 0x496E6B6D

// paperSize は紙の画像の一辺。盤面の大きさによらず固定で、描くときに拡大する。
const paperSize = 512

// newPaper は w×h の紙の画像を作る。角は直角のままで、角丸は描画側の
// cutBoardCorners が地の色で削る。
//
// 地合いは2つの周波数の値ノイズを合成したもので、乱数は位置から決める
// （位置を乗算とシフト XOR で混ぜる整数ハッシュ。線形合同法ではない）。**配列を持たないのは、画面サイズぶんの
// 中間バッファを確保しないためである。**
func newPaper(w, h int) *ebiten.Image {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	img := ebiten.NewImage(w, h)
	px := make([]byte, 4*w*h)
	base := ui.ColorPaper
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := 4 * (y*w + x)
			n := grainAt(x, y)
			px[i] = clampByte(int(base.R) + n)
			px[i+1] = clampByte(int(base.G) + n)
			px[i+2] = clampByte(int(base.B) + n)
			px[i+3] = 0xFF
		}
	}
	img.WritePixels(px)
	return img
}

// grainAt は位置から地合いの明暗（-paperGrain..+paperGrain）を返す。
// 粗い格子と細かい格子の2つを足し、紙の繊維らしい不均一さを出す。
func grainAt(x, y int) int {
	coarse := hash2(x>>2, y>>2)
	fine := hash2(x, y)
	// coarse を主、fine を従にする。合計の振幅が paperGrain を超えないよう
	// 2:1 で配分する。
	v := (coarse%(2*paperGrain+1))*2 + (fine % (2*paperGrain + 1))
	return v/3 - paperGrain
}

// hash2 は2つの整数から 0 以上の擬似乱数を作る。
func hash2(x, y int) int {
	v := uint32(x)*0x9E3779B1 ^ uint32(y)*0x85EBCA77 ^ uint32(paperSeed)
	v ^= v >> 15
	v *= 0x2545F491
	v ^= v >> 13
	return int(v & 0x7FFFFFFF)
}

func clampByte(v int) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}
