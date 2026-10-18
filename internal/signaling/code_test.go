package signaling

import (
	"strings"
	"testing"
)

func TestNewCodeShape(t *testing.T) {
	for i := 0; i < 200; i++ {
		c, err := NewCode()
		if err != nil {
			t.Fatalf("NewCode: %v", err)
		}
		if len(c) != CodeLength {
			t.Fatalf("長さ = %d, 期待 %d (%q)", len(c), CodeLength, c)
		}
		for _, r := range c {
			if !strings.ContainsRune(CodeAlphabet, r) {
				t.Fatalf("アルファベット外の文字 %q が出た (%q)", r, c)
			}
		}
	}
}

// 口頭で伝える前提なので、読み違えやすい文字は入っていてはならない。
func TestCodeAlphabetExcludesAmbiguousRunes(t *testing.T) {
	for _, r := range "01OIL" {
		if strings.ContainsRune(CodeAlphabet, r) {
			t.Errorf("紛らわしい文字 %q がアルファベットに含まれている", r)
		}
	}
	if len(CodeAlphabet) != 31 {
		t.Errorf("アルファベットの文字数 = %d, 期待 31", len(CodeAlphabet))
	}
	seen := map[rune]bool{}
	for _, r := range CodeAlphabet {
		if seen[r] {
			t.Errorf("アルファベットに重複 %q", r)
		}
		seen[r] = true
	}
}

// 剰余による偏りが無いこと。剰余ベースの選択に戻るリグレッションを検出する。
// 剰余実装（randomByte % 31）では、256 = 8*31 + 8 なので、最初の8文字が 9/256 の確率、
// 他の23文字が 8/256 の確率になり、カイ二乗統計は 336.9 になる。
// 30 自由度で p=1e-6 の臨界値は約 90.0 なので、このしきい値は剰余偏りを
// 大きな余裕を持って検出でき（336.9 vs 90.0）、正規実装では約百万分の一で誤検出する。
func TestNewCodeHasNoModuloBias(t *testing.T) {
	const n = 20000
	count := map[rune]int{}
	for i := 0; i < n; i++ {
		c, err := NewCode()
		if err != nil {
			t.Fatalf("NewCode: %v", err)
		}
		for _, r := range c {
			count[r]++
		}
	}

	total := n * CodeLength
	expected := float64(total) / float64(len(CodeAlphabet))

	var chi2 float64
	for _, r := range CodeAlphabet {
		obs := float64(count[r])
		chi2 += (obs - expected) * (obs - expected) / expected
	}

	if chi2 > 90.0 {
		t.Errorf("カイ二乗統計 = %.1f (> 90.0、剰余偏りの兆候)", chi2)
	}
}
