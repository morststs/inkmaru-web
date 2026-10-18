package ui

import "image"

// 縦持ちのスマートフォンを基準にした各段の高さ。
const (
	headerH   = 44
	scoreBarH = 40
	infoH     = 44
	buttonH   = 60
	gap       = 8
	sidePad   = 12
)

// Layout は画面の分割結果。盤面は常に正方形になる。
type Layout struct {
	Screen   image.Rectangle
	Header   image.Rectangle
	ScoreBar image.Rectangle
	Board    image.Rectangle
	Info     image.Rectangle
	Button   image.Rectangle
}

// ComputeLayout は画面サイズから各領域を決める。
// 盤面は「横幅」と「上下のUIを引いた残りの高さ」の小さいほうを一辺にし、
// 横方向は中央寄せ、縦方向は残り領域の中央に置く。
// ブラウザのビューポートが小さく圧縮されたサイズでも、すべての矩形が画面内に
// 収まり、重ならないように調整される。
func ComputeLayout(w, h int) Layout {
	var l Layout
	l.Screen = image.Rect(0, 0, w, h)

	clampRect := func(r image.Rectangle) image.Rectangle {
		if r.Min.X < 0 {
			r.Min.X = 0
		}
		if r.Min.Y < 0 {
			r.Min.Y = 0
		}
		if r.Max.X > w {
			r.Max.X = w
		}
		if r.Max.Y > h {
			r.Max.Y = h
		}
		if r.Min.X > r.Max.X {
			r.Max.X = r.Min.X
		}
		if r.Min.Y > r.Max.Y {
			r.Max.Y = r.Min.Y
		}
		return r
	}

	y := 0
	l.Header = clampRect(image.Rect(sidePad, y, w-sidePad, y+headerH))
	y = l.Header.Max.Y + gap
	if y > h {
		y = h
	}

	l.ScoreBar = clampRect(image.Rect(sidePad, y, w-sidePad, y+scoreBarH))
	y = l.ScoreBar.Max.Y + gap
	if y > h {
		y = h
	}

	iy := h - buttonH - gap - infoH
	avail := iy - gap - y
	side := w - 2*sidePad
	if avail < side {
		side = avail
	}
	if side < 1 {
		side = 1
	}
	bx := (w - side) / 2
	by := y + (avail-side)/2
	if by < y {
		by = y
	}
	l.Board = clampRect(image.Rect(bx, by, bx+side, by+side))
	// 盤面は常に正方形でなければならないため、幅と高さの小さいほうに揃える。
	boardW := l.Board.Dx()
	boardH := l.Board.Dy()
	minSide := boardW
	if boardH < minSide {
		minSide = boardH
	}
	l.Board.Max.X = l.Board.Min.X + minSide
	l.Board.Max.Y = l.Board.Min.Y + minSide

	y = l.Board.Max.Y + gap
	if y > h {
		y = h
	}

	// iy がボード領域と重ならないように調整
	if iy < l.Board.Max.Y {
		iy = l.Board.Max.Y
	}
	// 画面境界内に収める
	if iy < 0 {
		iy = 0
	}
	if iy > h {
		iy = h
	}

	// Info と Button が重ならないように調整
	infoMaxY := iy + infoH
	buttonMinY := h - buttonH
	// Button は少なくとも Info の終わり以降に来る
	if buttonMinY < infoMaxY {
		buttonMinY = infoMaxY
	}

	l.Info = clampRect(image.Rect(sidePad, iy, w-sidePad, infoMaxY))
	l.Button = clampRect(image.Rect(sidePad, buttonMinY, w-sidePad, h))

	return l
}
