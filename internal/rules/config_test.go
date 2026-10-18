package rules

import (
	"math"
	"testing"
)

func TestDefaultConfigDensityRange(t *testing.T) {
	c := DefaultConfig()
	// インク量と最大円の面積が等しいので、最も薄い円の濃度はちょうど 1.0 になる。
	if d := Density(c.InkPerTurn, c.MaxRadius()); math.Abs(d-1.0) > 1e-9 {
		t.Errorf("最大円の濃度 = %v, 期待 1.0", d)
	}
	// 最小円は面積が 0.03 なので 0.47/0.03 = 15.6667 倍濃い。
	if d := Density(c.InkPerTurn, c.MinRadius()); math.Abs(d-15.666666) > 1e-4 {
		t.Errorf("最小円の濃度 = %v, 期待 15.6667", d)
	}
}

func TestClampCircle(t *testing.T) {
	c := DefaultConfig()
	got := c.ClampCircle(Circle{CX: -0.5, CY: 1.8, R: 99})
	if got.CX != 0 || got.CY != 1 {
		t.Errorf("中心が盤面内に丸められていない: %+v", got)
	}
	if math.Abs(got.R-c.MaxRadius()) > 1e-12 {
		t.Errorf("半径 = %v, 期待 %v", got.R, c.MaxRadius())
	}
	if got := c.ClampCircle(Circle{CX: .5, CY: .5, R: 0}); math.Abs(got.R-c.MinRadius()) > 1e-12 {
		t.Errorf("半径 = %v, 期待 %v", got.R, c.MinRadius())
	}
}

func TestInkForTurnUsesFinalMultiplier(t *testing.T) {
	c := DefaultConfig()
	if got := c.InkForTurn(2); got != c.InkPerTurn {
		t.Errorf("既定では最終ターンも等倍のはず: %v", got)
	}
	c.FinalTurnInkMultiplier = 1.5
	if got, want := c.InkForTurn(2), c.InkPerTurn*1.5; math.Abs(got-want) > 1e-12 {
		t.Errorf("最終ターン = %v, 期待 %v", got, want)
	}
	if got := c.InkForTurn(0); got != c.InkPerTurn {
		t.Errorf("最終ターン以外は等倍のはず: %v", got)
	}
}
