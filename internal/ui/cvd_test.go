package ui

// 色覚多様性の下での見分けやすさと、文字のコントラストを測る。
//
// **これはテストファイルにのみ置く。** 測定は配布物に要らない（ブラウザに
// 配る wasm に入れる理由が無い）。
//
// 変換は Viénot, Brettel & Mollon (1999) の線形近似。sRGB → 線形 → LMS へ
// 送り、該当する錐体の軸へ潰してから戻す。ΔE は CIE76。
//
// **精密な医学的模擬ではない。** 目的は「配色を触ったときに、見分けにくく
// なったことに気づく」ことであって、絶対値の正しさではない。だから閾値は
// 旧配色の実測値（8.3、T型の P1-中立）のすぐ上に置いた丸い値（8.5）である。
// 新配色の実測は 9.30（同じく T型の P1-中立）で、下限との間に余裕がある。

import (
	"image/color"
	"math"
	"testing"

	"github.com/morststs/inkmaru-web/internal/rules"
)

func srgbToLin(c float64) float64 {
	c /= 255
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func linToSrgb(c float64) float64 {
	c = math.Max(0, math.Min(1, c))
	if c <= 0.0031308 {
		return c * 12.92 * 255
	}
	return (1.055*math.Pow(c, 1/2.4) - 0.055) * 255
}

type vec3 [3]float64
type mat3 [3][3]float64

func (m mat3) mul(v vec3) vec3 {
	var o vec3
	for i := 0; i < 3; i++ {
		o[i] = m[i][0]*v[0] + m[i][1]*v[1] + m[i][2]*v[2]
	}
	return o
}

var (
	rgbToLMS = mat3{{0.31399022, 0.63951294, 0.04649755}, {0.15537241, 0.75789446, 0.08670142}, {0.01775239, 0.10944209, 0.87256922}}
	lmsToRGB = mat3{{5.47221206, -4.6419601, 0.16963708}, {-1.1252419, 2.29317094, -0.1678952}, {0.02980165, -0.19318073, 1.16364789}}
	identity = mat3{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
	protan   = mat3{{0, 1.05118294, -0.05116099}, {0, 1, 0}, {0, 0, 1}}
	deutan   = mat3{{1, 0, 0}, {0.9513092, 0, 0.04866992}, {0, 0, 1}}
	tritan   = mat3{{1, 0, 0}, {0, 1, 0}, {-0.86744736, 1.86727089, 0}}
)

var visions = []struct {
	name string
	m    mat3
}{{"正常", identity}, {"P型", protan}, {"D型", deutan}, {"T型", tritan}}

func simulateCVD(c vec3, m mat3) vec3 {
	lin := vec3{srgbToLin(c[0]), srgbToLin(c[1]), srgbToLin(c[2])}
	out := lmsToRGB.mul(m.mul(rgbToLMS.mul(lin)))
	return vec3{linToSrgb(out[0]), linToSrgb(out[1]), linToSrgb(out[2])}
}

func labOf(c vec3) vec3 {
	r, g, b := srgbToLin(c[0]), srgbToLin(c[1]), srgbToLin(c[2])
	x := 0.4124564*r + 0.3575761*g + 0.1804375*b
	y := 0.2126729*r + 0.7151522*g + 0.0721750*b
	z := 0.0193339*r + 0.1191920*g + 0.9503041*b
	f := func(t float64) float64 {
		if t > math.Pow(6.0/29, 3) {
			return math.Cbrt(t)
		}
		return t/(3*math.Pow(6.0/29, 2)) + 4.0/29
	}
	fx, fy, fz := f(x/0.95047), f(y), f(z/1.08883)
	return vec3{116*fy - 16, 500 * (fx - fy), 200 * (fy - fz)}
}

func deltaE(a, b vec3) float64 {
	la, lb := labOf(a), labOf(b)
	return math.Sqrt((la[0]-lb[0])*(la[0]-lb[0]) + (la[1]-lb[1])*(la[1]-lb[1]) + (la[2]-lb[2])*(la[2]-lb[2]))
}

func toVec(c color.NRGBA) vec3 { return vec3{float64(c.R), float64(c.G), float64(c.B)} }

func over(paper, ink vec3, a float64) vec3 {
	return vec3{
		paper[0]*(1-a) + ink[0]*a,
		paper[1]*(1-a) + ink[1]*a,
		paper[2]*(1-a) + ink[2]*a,
	}
}

func relLum(c vec3) float64 {
	return 0.2126*srgbToLin(c[0]) + 0.7152*srgbToLin(c[1]) + 0.0722*srgbToLin(c[2])
}

func contrastRatio(fg, bg color.NRGBA) float64 {
	a, b := relLum(toVec(fg)), relLum(toVec(bg))
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05)
}

// minSeparation は、盤面に実際に出る面どうしの ΔE の下限。
// 持ち主が同じ面どうし（自分の陣地の濃淡差）は見分けなくてよいので除く。
//
// 8.5 は旧配色の実測値そのものではない。旧配色の最小は 8.3（T型の P1-中立、
// boardSurfaces の標本で測った値）で、8.5 はその上に置いた丸い下限である。
// 新配色の実測は 9.30。
const minSeparation = 8.5

type surface struct {
	owner rules.Player
	name  string
	c     vec3
}

// boardSurfaces は盤面に出る色を全部並べる。
// internal/ui/boardview.go の描き方と一致させること。
func boardSurfaces() []surface {
	paper := toVec(ColorPaper)
	out := []surface{{rules.None, "未取得", paper}}
	for _, a := range []float64{AlphaLo, 0.40, 0.60, AlphaHi} {
		out = append(out,
			surface{rules.P1, "P1", over(paper, toVec(ColorP1), a)},
			surface{rules.P2, "P2", over(paper, toVec(ColorP2), a)})
	}
	for _, a := range []float64{neutralAlphaOff, neutralAlphaOn} {
		out = append(out, surface{rules.Neutral, "中立", over(paper, toVec(ColorNeutral), a)})
	}
	return out
}

// 色覚多様性の下でも、持ち主の違う面が見分けられること。
//
// **これまでこの配慮はコメントに書いてあるだけで、何にも守られていなかった。**
// 配色を触ったときにここが落ちる。落ちたら配色を戻すか、閾値を下げてよい
// 根拠（実測）を添えて書き換えること。
func TestPaletteSeparationUnderColorVisionDeficiency(t *testing.T) {
	ss := boardSurfaces()
	for _, v := range visions {
		sim := make([]vec3, len(ss))
		for i, s := range ss {
			sim[i] = simulateCVD(s.c, v.m)
		}
		for i := range ss {
			for j := i + 1; j < len(ss); j++ {
				if ss[i].owner == ss[j].owner {
					continue
				}
				if d := deltaE(sim[i], sim[j]); d < minSeparation {
					t.Errorf("%s: %s と %s の ΔE = %.1f（下限 %.1f）",
						v.name, ss[i].name, ss[j].name, d, minSeparation)
				}
			}
		}
	}
}

// ボタンの塗りと、その上に載る文字が読めること。
//
// **陣地の色をボタンの塗りに流用すると落ちる。** 流用していた旧実装では
// 朱の上の白文字が 2.57:1 しかなかった（仕様書 §3.4 の実測）。
// 陣地の色は「薄く重ねても見分けられること」で選び、ボタンの色は
// 「上に載る文字が読めること」で選ぶ。条件が違うので同じ色にはならない。
func TestButtonTextContrast(t *testing.T) {
	// WCAG 2.1: 本文 4.5:1、大きい文字（24px 以上）3:1。
	//
	// 「押せないボタン」だけ 3:1 にしている根拠は「大きい文字だから」ではない。
	// **gfx の fitScale は、幅に収まらない文字を 18px（「部屋を作る」は倍率 1.5）まで
	// 落としうる**ので、24px の基準はもう成り立たない。本当の根拠は、操作できない
	// UI 部品の文字は WCAG 1.4.3 の適用除外（Incidental）だということである
	// （https://www.w3.org/WAI/WCAG22/Understanding/contrast-minimum.html）。
	// このボタンは見た目だけでなく実際に反応しない（ui.OnlineButtons が
	// EmphasisDisabled を返し、押下側もそれを見て弾く）ので当たる。
	// 3:1 は自ら課した床で、WCAG が要求する値ではない。
	cases := []struct {
		name    string
		fg, bg  color.NRGBA
		minimum float64
	}{
		{"主ボタン", ColorText, ColorButtonPrimary, 4.5},
		{"タイトルのボタン", ColorText, ColorButtonTitle, 4.5},
		{"押せないボタン", ColorTextDisabled, ColorButtonDisabled, 3.0},
		{"地の上の文字", ColorText, ColorScreenBG, 4.5},
	}
	for _, c := range cases {
		if got := contrastRatio(c.fg, c.bg); got < c.minimum {
			t.Errorf("%s: コントラスト比 %.2f:1（下限 %.1f:1）", c.name, got, c.minimum)
		}
	}
}

// 朱はボタンの塗りに使わない。相手の色なので意味が混ざるうえ、
// 生成りの文字を載せると 2.74:1 しかなく読めない（仕様書 §3.4）。
func TestVermilionIsNotAButtonFill(t *testing.T) {
	for _, bg := range []color.NRGBA{ColorButtonPrimary, ColorButtonTitle, ColorButtonDisabled} {
		if bg == ColorP2 {
			t.Errorf("朱 %v がボタンの塗りに使われている", bg)
		}
	}
}
