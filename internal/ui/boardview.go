package ui

import (
	"image/color"

	"github.com/morststs/inkmaru-web/internal/rules"
)

// 中立セルは濃度ではなく固定の2値で縞を描く。
// 中立は誰の陣地でもないので濃度に戦略的な意味がなく、それよりも
// 「相討ちで死んだ領域」だと一目で分かることのほうが重要である。
// 濃度から導くと薄い陣地では縞の濃淡差が小さく潰れ、陣地との区別がつきにくい。
// 値は目で決めた調整値で、どちらも AlphaHi 以下に収めてある
// （縁を足しても cellAlpha が AlphaHi で頭打ちにする）。
const (
	neutralAlphaOn  = 0.70
	neutralAlphaOff = 0.28
)

// edgeGain は持ち主の変わる境界で足す不透明度。インクが縁に溜まる見え方。
//
// 値そのものは目で決めた調整値である。**守るべきは値ではなく、
// cellAlpha が AlphaHi で頭打ちにすること**（boardview_test.go が固定する）。
const edgeGain = 0.10

// RenderBoardPixels は盤面を RGBA バイト列に変換する。
//
// **紙は描かない。** 未取得のセルは完全な透明（0,0,0,0）で書き出し、
// internal/gfx が下に敷く紙の層が透けるようにする。かつてはここで紙の色を
// 合成して A=255 で書き出していたが、それでは紙の地合いが完全に隠れる。
//
// **ebiten.Image.WritePixels は premultiplied alpha を要求する。**
// A=255 だった頃は premultiplied 値と素の値が一致していたので取り違えようが
// なかったが、半透明にすると一致しない。間違えても落ちず、色が濃すぎる／
// 薄すぎるだけの壊れ方をするので、boardview_test.go で固定してある。
//
// dst に十分な容量があれば再利用する。毎ターン1回しか呼ばないが、
// 65,536 セル分の確保を繰り返さないようにしている。
func RenderBoardPixels(b *rules.Board, dst []byte) []byte {
	n := b.GridN()
	need := 4 * n * n
	if cap(dst) < need {
		dst = make([]byte, need)
	}
	dst = dst[:need]

	cfg := b.Config()
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			i := 4 * (y*n + x)
			owner := b.OwnerAt(x, y)
			if owner == rules.None {
				dst[i], dst[i+1], dst[i+2], dst[i+3] = 0, 0, 0, 0
				continue
			}
			a := cellAlpha(cfg, b, owner, x, y)
			writePremultiplied(dst, i, PlayerColor(owner), a)
		}
	}
	return dst
}

// cellAlpha はセルの表示用不透明度を返す。
//
// 中立は斜めのハッチングで描き、相討ちが起きた場所を一目で分かるようにする。
// 陣地は濃度から引く（AlphaForDensity が単調なので「濃く見えるほうが強い」が保たれる）。
// 持ち主の変わる境界には edgeGain を足す。
func cellAlpha(cfg rules.Config, b *rules.Board, owner rules.Player, x, y int) float64 {
	var a float64
	if owner == rules.Neutral {
		if (x+y)>>2&1 == 1 {
			a = neutralAlphaOn
		} else {
			a = neutralAlphaOff
		}
	} else {
		a = AlphaForDensity(cfg, b.DensityAt(x, y))
	}
	if isOwnerBoundary(b, x, y, owner) {
		a += edgeGain
	}
	// **必ず頭打ちにする。** これが無いと薄い陣地の縁が濃い陣地の内側より
	// 濃く見え、画面がルールについて嘘をつく。
	if a > AlphaHi {
		a = AlphaHi
	}
	return a
}

// isOwnerBoundary は (x,y) の四近傍に、持ち主の違うセルがあるかを返す。
//
// 盤面の外も「違う持ち主」とみなす。紙との境目にもインクは溜まるためである。
func isOwnerBoundary(b *rules.Board, x, y int, owner rules.Player) bool {
	n := b.GridN()
	for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		nx, ny := x+d[0], y+d[1]
		if nx < 0 || ny < 0 || nx >= n || ny >= n {
			return true
		}
		if b.OwnerAt(nx, ny) != owner {
			return true
		}
	}
	return false
}

// writePremultiplied は色と不透明度を premultiplied RGBA として書き込む。
func writePremultiplied(dst []byte, i int, c color.NRGBA, a float64) {
	if a <= 0 {
		dst[i], dst[i+1], dst[i+2], dst[i+3] = 0, 0, 0, 0
		return
	}
	if a > 1 {
		a = 1
	}
	av := uint8(a*255 + 0.5)
	f := float64(av) / 255
	dst[i] = uint8(float64(c.R)*f + 0.5)
	dst[i+1] = uint8(float64(c.G)*f + 0.5)
	dst[i+2] = uint8(float64(c.B)*f + 0.5)
	dst[i+3] = av
}
