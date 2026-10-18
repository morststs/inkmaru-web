package ui

// InkBleedRadii はインクの円のにじみの層の半径の倍率。外側から内側の順に並べる。
// InkLayerAlphas が返す不透明度と添字で対応する。
var InkBleedRadii = [...]float64{1.06, 1.02, 1.00}

// inkOuterWeights は、外側の層の不透明度が a の何倍か。最も内側の層は
// 持たない（InkLayerAlphas が合成から逆算する）。
var inkOuterWeights = [len(InkBleedRadii) - 1]float64{0.18, 0.34}

// InkLayerAlphas は、にじみのある円を描く各層の不透明度を返す。
// 長さは InkBleedRadii と同じで、添字も対応する（外側から内側）。
//
// **層を SourceOver で重ねた中心の合成不透明度 1-Π(1-αᵢ) が、ちょうど a になる。**
// 外側の層を a の定数倍で決め、最も内側の層をそこから逆算する。
// 3層をそれぞれ a の倍率で足すと合成は a を超え（a=0.88 で 0.93）、確定前の円が
// 確定後の陣地（AlphaHi が上限）より濃く見えてしまう。
// a は 0..1 に丸める。
func InkLayerAlphas(a float64) []float64 {
	if a < 0 {
		a = 0
	}
	if a > 1 {
		a = 1
	}
	out := make([]float64, len(InkBleedRadii))
	rest := 1.0
	for i, w := range inkOuterWeights {
		out[i] = a * w
		rest *= 1 - out[i]
	}
	inner := 1 - (1-a)/rest
	if inner < 0 {
		inner = 0
	}
	if inner > 1 {
		inner = 1
	}
	out[len(out)-1] = inner
	return out
}
