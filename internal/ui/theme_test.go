package ui

import (
	"testing"

	"github.com/morststs/inkmaru-web/internal/rules"
)

func TestAlphaForDensityIsMonotonic(t *testing.T) {
	cfg := rules.DefaultConfig()
	prev := -1.0
	for i := 0; i <= 20; i++ {
		r := cfg.MaxRadius() + (cfg.MinRadius()-cfg.MaxRadius())*float64(i)/20
		a := AlphaForDensity(cfg, rules.Density(cfg.InkPerTurn, r))
		if a <= prev {
			t.Fatalf("小さい円ほど濃くなっていない: r=%v alpha=%v prev=%v", r, a, prev)
		}
		prev = a
	}
}

func TestAlphaForDensityEndpoints(t *testing.T) {
	cfg := rules.DefaultConfig()
	lo := AlphaForDensity(cfg, rules.Density(cfg.InkPerTurn, cfg.MaxRadius()))
	hi := AlphaForDensity(cfg, rules.Density(cfg.InkPerTurn, cfg.MinRadius()))
	if lo < 0.20 || lo > 0.25 {
		t.Errorf("最大円の不透明度 = %v, 期待 0.22 前後（薄すぎると見えない）", lo)
	}
	if hi < 0.85 || hi > 0.90 {
		t.Errorf("最小円の不透明度 = %v, 期待 0.88 前後", hi)
	}
}

func TestAlphaForDensityClampsOutOfRange(t *testing.T) {
	cfg := rules.DefaultConfig()
	if a := AlphaForDensity(cfg, 0); a < 0 || a > 1 {
		t.Errorf("範囲外の入力で不透明度が [0,1] を外れた: %v", a)
	}
	if a := AlphaForDensity(cfg, 1e9); a > 1 {
		t.Errorf("範囲外の入力で不透明度が 1 を超えた: %v", a)
	}
}
