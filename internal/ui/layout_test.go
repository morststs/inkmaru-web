package ui

import (
	"image"
	"testing"
)

func TestComputeLayoutBoardIsSquare(t *testing.T) {
	for _, sz := range [][2]int{{375, 667}, {390, 844}, {768, 1024}, {667, 375}} {
		l := ComputeLayout(sz[0], sz[1])
		if w, h := l.Board.Dx(), l.Board.Dy(); w != h {
			t.Errorf("%v: 盤面が正方形でない %dx%d", sz, w, h)
		}
	}
}

func TestComputeLayoutFitsInsideScreen(t *testing.T) {
	for _, sz := range [][2]int{{375, 667}, {390, 844}, {768, 1024}, {667, 375}} {
		l := ComputeLayout(sz[0], sz[1])
		for name, r := range map[string]struct{ x0, y0, x1, y1 int }{
			"Header":   {l.Header.Min.X, l.Header.Min.Y, l.Header.Max.X, l.Header.Max.Y},
			"ScoreBar": {l.ScoreBar.Min.X, l.ScoreBar.Min.Y, l.ScoreBar.Max.X, l.ScoreBar.Max.Y},
			"Board":    {l.Board.Min.X, l.Board.Min.Y, l.Board.Max.X, l.Board.Max.Y},
			"Info":     {l.Info.Min.X, l.Info.Min.Y, l.Info.Max.X, l.Info.Max.Y},
			"Button":   {l.Button.Min.X, l.Button.Min.Y, l.Button.Max.X, l.Button.Max.Y},
		} {
			if r.x0 < 0 || r.y0 < 0 || r.x1 > sz[0] || r.y1 > sz[1] {
				t.Errorf("%v: %s が画面からはみ出した %+v", sz, name, r)
			}
		}
	}
}

func TestComputeLayoutSectionsDoNotOverlap(t *testing.T) {
	l := ComputeLayout(390, 844)
	order := []struct {
		name string
		y0   int
		y1   int
	}{
		{"Header", l.Header.Min.Y, l.Header.Max.Y},
		{"ScoreBar", l.ScoreBar.Min.Y, l.ScoreBar.Max.Y},
		{"Board", l.Board.Min.Y, l.Board.Max.Y},
		{"Info", l.Info.Min.Y, l.Info.Max.Y},
		{"Button", l.Button.Min.Y, l.Button.Max.Y},
	}
	for i := 1; i < len(order); i++ {
		if order[i].y0 < order[i-1].y1 {
			t.Errorf("%s が %s と重なっている", order[i].name, order[i-1].name)
		}
	}
}

func TestComputeLayoutBoardIsCentredHorizontally(t *testing.T) {
	l := ComputeLayout(768, 1024)
	left := l.Board.Min.X
	right := 768 - l.Board.Max.X
	if left != right {
		t.Errorf("盤面が水平方向に中央寄せされていない: 左%d 右%d", left, right)
	}
}

func TestComputeLayoutSurvivesDegenerateSizes(t *testing.T) {
	for _, sz := range [][2]int{{390, 300}, {390, 200}, {390, 100}, {200, 200}, {100, 100}, {1, 1}, {0, 0}} {
		l := ComputeLayout(sz[0], sz[1])
		screen := image.Rect(0, 0, sz[0], sz[1])

		// 不変条件1: すべての矩形が画面内に収まる
		for name, r := range map[string]image.Rectangle{
			"Header":   l.Header,
			"ScoreBar": l.ScoreBar,
			"Board":    l.Board,
			"Info":     l.Info,
			"Button":   l.Button,
		} {
			if r.Min.X < 0 || r.Min.Y < 0 || r.Max.X > sz[0] || r.Max.Y > sz[1] {
				t.Errorf("%v: %s が画面からはみ出した %+v", sz, name, r)
			}
		}

		// 不変条件2: 縦方向の順序を保つ、重ならない
		order := []struct {
			name string
			y0   int
			y1   int
		}{
			{"Header", l.Header.Min.Y, l.Header.Max.Y},
			{"ScoreBar", l.ScoreBar.Min.Y, l.ScoreBar.Max.Y},
			{"Board", l.Board.Min.Y, l.Board.Max.Y},
			{"Info", l.Info.Min.Y, l.Info.Max.Y},
			{"Button", l.Button.Min.Y, l.Button.Max.Y},
		}
		for i := 1; i < len(order); i++ {
			if order[i].y0 < order[i-1].y1 {
				t.Errorf("%v: %s が %s と重なっている (%d<%d)", sz, order[i].name, order[i-1].name, order[i].y0, order[i-1].y1)
			}
		}

		// 不変条件3: 盤面は常に正方形
		if w, h := l.Board.Dx(), l.Board.Dy(); w != h {
			t.Errorf("%v: 盤面が正方形でない %dx%d", sz, w, h)
		}

		// 不変条件4: スクリーンの矩形計算に整合性がある
		if !screen.In(l.Screen) && !(l.Screen.Dx() == 0 && l.Screen.Dy() == 0) {
			t.Errorf("%v: Screen矩形が一致していない", sz)
		}
	}
}
