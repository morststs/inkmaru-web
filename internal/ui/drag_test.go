package ui

import (
	"image"
	"math"
	"testing"

	"github.com/morststs/inkmaru-web/internal/rules"
)

func TestDragCircleCentreIsTouchStart(t *testing.T) {
	cfg := rules.DefaultConfig()
	area := image.Rect(20, 100, 220, 300) // 200x200
	c := DragCircle(cfg, area, 120, 200, 120, 200)
	if math.Abs(c.CX-0.5) > 1e-9 || math.Abs(c.CY-0.5) > 1e-9 {
		t.Errorf("中心 = (%v,%v), 期待 (0.5,0.5)", c.CX, c.CY)
	}
}

func TestDragCircleRadiusFromDistance(t *testing.T) {
	cfg := rules.DefaultConfig()
	area := image.Rect(0, 0, 200, 200)
	// 中心から 40px 動かすと、正規化半径は 40/200 = 0.2 になる。
	c := DragCircle(cfg, area, 100, 100, 140, 100)
	if math.Abs(c.R-0.2) > 1e-9 {
		t.Errorf("半径 = %v, 期待 0.2", c.R)
	}
}

func TestDragCircleClampsRadius(t *testing.T) {
	cfg := rules.DefaultConfig()
	area := image.Rect(0, 0, 200, 200)
	if c := DragCircle(cfg, area, 100, 100, 100, 100); math.Abs(c.R-cfg.MinRadius()) > 1e-9 {
		t.Errorf("動かさなかったときの半径 = %v, 期待 %v", c.R, cfg.MinRadius())
	}
	if c := DragCircle(cfg, area, 100, 100, 100, 1000); math.Abs(c.R-cfg.MaxRadius()) > 1e-9 {
		t.Errorf("大きく動かしたときの半径 = %v, 期待 %v", c.R, cfg.MaxRadius())
	}
}

func TestDragCircleClampsCentreIntoBoard(t *testing.T) {
	cfg := rules.DefaultConfig()
	area := image.Rect(0, 0, 200, 200)
	c := DragCircle(cfg, area, -50, 260, 0, 260)
	if c.CX != 0 || c.CY != 1 {
		t.Errorf("中心 = (%v,%v), 期待 (0,1)", c.CX, c.CY)
	}
}

func TestBoardToScreenRoundTrip(t *testing.T) {
	area := image.Rect(20, 100, 220, 300)
	x, y := BoardToScreen(area, 0.5, 0.25)
	if x != 120 || y != 150 {
		t.Errorf("(%v,%v), 期待 (120,150)", x, y)
	}
	if r := RadiusToScreen(area, 0.25); r != 50 {
		t.Errorf("半径 = %v, 期待 50", r)
	}
}
