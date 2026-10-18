package rules

import "math"

// Player はセルの所有者。
type Player uint8

const (
	None    Player = 0
	P1      Player = 1
	P2      Player = 2
	Neutral Player = 3
)

func (p Player) String() string {
	switch p {
	case P1:
		return "P1"
	case P2:
		return "P2"
	case Neutral:
		return "Neutral"
	default:
		return "None"
	}
}

// Board は陣地の状態を保持する。
//
// 各セルについて保持するのは「そのセルを覆う円の最大濃度 maxD」と
// 「maxD を達成したプレイヤーの集合 mask」の2つだけである。
// 所有者を直接書き込まないことで、円を適用する順序によらず結果が
// 一意に定まることが保証される（仕様書 §3.4）。この不変条件は
// TestApplyOrderIndependence で固定している。
type Board struct {
	cfg  Config
	n    int
	maxD []float32
	mask []uint8 // bit0 = P1, bit1 = P2
}

func NewBoard(cfg Config) *Board {
	n := cfg.GridN
	return &Board{cfg: cfg, n: n, maxD: make([]float32, n*n), mask: make([]uint8, n*n)}
}

func (b *Board) GridN() int     { return b.n }
func (b *Board) Config() Config { return b.cfg }

func (b *Board) Clone() *Board {
	return &Board{
		cfg:  b.cfg,
		n:    b.n,
		maxD: append([]float32(nil), b.maxD...),
		mask: append([]uint8(nil), b.mask...),
	}
}

// Apply は円 c をプレイヤー p の手として盤面に反映する。
// 盤外にはみ出した部分にはセルが存在しないため、そのインクは失われる。
func (b *Board) Apply(c Circle, p Player, ink float64) {
	if p != P1 && p != P2 {
		return
	}
	bit := uint8(1) << (uint8(p) - 1)
	d := float32(Density(ink, c.R))
	n := float64(b.n)

	x0 := max(0, int(math.Floor((c.CX-c.R)*n)))
	x1 := min(b.n-1, int(math.Ceil((c.CX+c.R)*n)))
	y0 := max(0, int(math.Floor((c.CY-c.R)*n)))
	y1 := min(b.n-1, int(math.Ceil((c.CY+c.R)*n)))
	r2 := c.R * c.R

	for y := y0; y <= y1; y++ {
		fy := (float64(y)+0.5)/n - c.CY
		row := y * b.n
		for x := x0; x <= x1; x++ {
			fx := (float64(x)+0.5)/n - c.CX
			if fx*fx+fy*fy > r2 {
				continue
			}
			i := row + x
			switch {
			case d > b.maxD[i]:
				b.maxD[i], b.mask[i] = d, bit
			case d == b.maxD[i]:
				b.mask[i] |= bit
			}
		}
	}
}

func (b *Board) OwnerAt(x, y int) Player { return ownerOf(b.mask[y*b.n+x]) }

func (b *Board) DensityAt(x, y int) float64 { return float64(b.maxD[y*b.n+x]) }

func ownerOf(mask uint8) Player {
	switch mask {
	case 0:
		return None
	case 1:
		return P1
	case 2:
		return P2
	default:
		return Neutral
	}
}

// Score は陣地の集計結果。添字は Player。
type Score struct {
	Cells    [4]int
	Weighted [4]float64
	Total    int
}

// Ratio は盤面全体に対する割合。
func (s Score) Ratio(p Player) float64 { return float64(s.Cells[p]) / float64(s.Total) }

// Share は Weighted を基準にした、そのプレイヤーの取り分。
// Match.Result() は Weighted の大小で勝敗を決めるため、表示側もこの値を使えば
// 「表示は勝っているのに結果は負け」というズレが起きない。Share は勝敗判定の
// 表示用対応物であり、Ratio は素の面積比としてそのまま残す（用途が異なる）。
// DensityWeight == 0 なら Weighted の各値は Cells 数と等しくなるため、
// このとき Share は Ratio と完全に一致する。
func (s Score) Share(p Player) float64 {
	total := s.Weighted[None] + s.Weighted[P1] + s.Weighted[P2] + s.Weighted[Neutral]
	if total == 0 {
		return 0
	}
	return s.Weighted[p] / total
}

func (b *Board) Score() Score {
	s := Score{Total: b.n * b.n}
	w := b.cfg.DensityWeight
	for i, m := range b.mask {
		owner := ownerOf(m)
		s.Cells[owner]++
		v := 1.0
		if w != 0 {
			v = math.Pow(float64(b.maxD[i]), w)
		}
		s.Weighted[owner] += v
	}
	return s
}
