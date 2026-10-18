package ui

import (
	"image/color"
	"math"

	"github.com/morststs/inkmaru-web/internal/rules"
)

// 配色は「墨と紙」の調子に寄せてある。値の根拠と実測は
// docs/superpowers/specs/2026-10-10-inkmaru-visual-design.md §3 にある。
//
// **陣地の色とボタンの色は別である。** 陣地の色は「薄く重ねても持ち主を
// 見分けられること」で選び、ボタンの色は「上に載る文字が読めること」で
// 選ぶ。かつては ColorP1 を両方に流用しており、そのせいで文字の
// コントラストが WCAG の基準に届いていなかった（cvd_test.go が守る）。
var (
	// 陣地の色。藍と朱。色覚多様性の下での見分けやすさを測って選んである。
	ColorP1      = color.NRGBA{R: 0x25, G: 0x76, B: 0xCC, A: 0xFF}
	ColorP2      = color.NRGBA{R: 0xE2, G: 0x69, B: 0x1F, A: 0xFF}
	ColorNeutral = color.NRGBA{R: 0x6E, G: 0x68, B: 0x60, A: 0xFF}

	// ColorPaper は盤面の紙の色。生成り。
	ColorPaper = color.NRGBA{R: 0xF2, G: 0xED, B: 0xE1, A: 0xFF}
	// ColorScreenBG は画面の地。墨寄りの暖かい暗灰。
	ColorScreenBG = color.NRGBA{R: 0x20, G: 0x1E, B: 0x1B, A: 0xFF}
	// ColorText は地の上に載る文字。紙に寄せた白。
	ColorText = color.NRGBA{R: 0xED, G: 0xE8, B: 0xDE, A: 0xFF}

	// ColorButtonPrimary は主ボタンの塗り。深藍。
	// **ColorP1 より深い。** 上に ColorText を載せて 5.29:1 を確保するため。
	ColorButtonPrimary = color.NRGBA{R: 0x1C, G: 0x5F, B: 0xA8, A: 0xFF}
	// ColorButtonTitle はタイトル画面など、副次的なボタンの塗り。
	ColorButtonTitle = color.NRGBA{R: 0x2E, G: 0x2A, B: 0x25, A: 0xFF}
	// ColorButtonDisabled は押せないボタンの塗り。
	ColorButtonDisabled = color.NRGBA{R: 0x33, G: 0x30, B: 0x2B, A: 0xFF}
	// ColorTextDisabled は押せないボタンの文字。
	ColorTextDisabled = color.NRGBA{R: 0x8A, G: 0x85, B: 0x7C, A: 0xFF}
)

func PlayerColor(p rules.Player) color.NRGBA {
	switch p {
	case rules.P1:
		return ColorP1
	case rules.P2:
		return ColorP2
	case rules.Neutral:
		return ColorNeutral
	default:
		return ColorPaper
	}
}

// 表示用の不透明度のレンジとガンマ。
//
// AlphaLo・AlphaHi を公開しているのは、インクの縁の頭打ち（boardview.go）と
// 見分けやすさの測定（cvd_test.go）が同じ値を見る必要があるためである。
const (
	AlphaLo    = 0.22
	AlphaHi    = 0.88
	alphaGamma = 0.45
)

// AlphaForDensity は濃度を表示用の不透明度に変換する。
//
// 真の濃度をそのまま不透明度にすると最大の円が薄すぎて見えなくなるため、
// ガンマ補正して見やすい範囲に写す。単調増加変換なので「濃く見えるほうが
// 強い」という見た目とルールの一致は保たれる（仕様書 §3.7）。
func AlphaForDensity(cfg rules.Config, d float64) float64 {
	dMin := rules.Density(cfg.InkPerTurn, cfg.MaxRadius())
	dMax := rules.Density(cfg.InkPerTurn, cfg.MinRadius())
	t := (d - dMin) / (dMax - dMin)
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return AlphaLo + (AlphaHi-AlphaLo)*math.Pow(t, alphaGamma)
}
