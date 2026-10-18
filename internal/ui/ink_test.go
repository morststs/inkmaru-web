package ui

import (
	"math"
	"testing"
)

// composite は各層を SourceOver で重ねた中心の不透明度。
func composite(as []float64) float64 {
	t := 1.0
	for _, a := range as {
		t *= 1 - a
	}
	return 1 - t
}

func inkSweep(f func(a float64)) {
	for a := 0.0; a <= 1.0+1e-9; a += 0.01 {
		f(a)
	}
}

// 合成が a に一致する。インクの円は確定前も確定後も同じ濃さに見えなければならない。
func TestInkCompositeEqualsAlpha(t *testing.T) {
	n := 0
	for a := 0.22; a <= 0.88+1e-9; a += 0.01 {
		n++
		if got := composite(InkLayerAlphas(a)); math.Abs(got-a) > 1e-9 {
			t.Errorf("a=%.2f 合成=%.6f", a, got)
		}
	}
	if n < 60 { // 走査が効いたことの下限
		t.Fatalf("走査が %d 点しかない", n)
	}
}

// 合成が AlphaHi を超えない。
func TestInkCompositeNeverExceedsAlphaHi(t *testing.T) {
	hi := float64(AlphaHi)
	inkSweep(func(a float64) {
		if a > hi {
			return
		}
		if got := composite(InkLayerAlphas(a)); got > hi+1e-9 {
			t.Errorf("a=%.2f 合成=%.6f > AlphaHi=%.2f", a, got, hi)
		}
	})
}

// 合成は a について単調増加。濃く見えるほうが強い、を守る。
func TestInkCompositeMonotonic(t *testing.T) {
	prev := -1.0
	inkSweep(func(a float64) {
		got := composite(InkLayerAlphas(a))
		if got <= prev {
			t.Errorf("a=%.2f 合成=%.6f が直前 %.6f 以下", a, got, prev)
		}
		prev = got
	})
}

// 層の数が半径の倍率と一致し、外側ほど薄い。
func TestInkLayersMatchRadiiAndFadeOutward(t *testing.T) {
	inkSweep(func(a float64) {
		as := InkLayerAlphas(a)
		if len(as) != len(InkBleedRadii) {
			t.Fatalf("層 %d, 半径 %d", len(as), len(InkBleedRadii))
		}
		if a == 0 {
			return
		}
		for i := 1; i < len(as); i++ {
			if as[i-1] >= as[i] {
				t.Errorf("a=%.2f 外側 %v が内側 %v 以上", a, as[i-1], as[i])
			}
		}
	})
	for i := 1; i < len(InkBleedRadii); i++ {
		if InkBleedRadii[i-1] <= InkBleedRadii[i] {
			t.Errorf("半径の倍率が外側から減っていない: %v", InkBleedRadii)
		}
	}
}

func TestInkLayerBounds(t *testing.T) {
	for i, v := range InkLayerAlphas(0) {
		if v != 0 {
			t.Errorf("a=0 の層 %d = %v", i, v)
		}
	}
	as := InkLayerAlphas(1)
	if math.Abs(as[len(as)-1]-1) > 1e-12 {
		t.Errorf("a=1 の内側 = %v", as[len(as)-1])
	}
}
