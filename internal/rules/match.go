package rules

import "errors"

var (
	ErrAlreadySubmitted = errors.New("rules: このターンには既に提出済み")
	ErrMatchFinished    = errors.New("rules: 対戦は終了している")
	ErrInvalidPlayer    = errors.New("rules: プレイヤーが不正")
)

// Move は確定した1手。履歴として保持し、CPU AI にはこれだけを渡す。
type Move struct {
	Circle Circle
	Player Player
	Ink    float64
}

// BuildBoard は履歴から盤面を組み立てる。
// GridN を変えた Config を渡せば低解像度の盤面が得られるため、
// CPU AI が探索用の粗い盤面を作るのに使う。
func BuildBoard(cfg Config, hist []Move) *Board {
	b := NewBoard(cfg)
	for _, mv := range hist {
		b.Apply(mv.Circle, mv.Player, mv.Ink)
	}
	return b
}

// Result は対戦の結果。
type Result uint8

const (
	InProgress Result = iota
	WinP1
	WinP2
	Draw
)

func (r Result) String() string {
	switch r {
	case WinP1:
		return "WinP1"
	case WinP2:
		return "WinP2"
	case Draw:
		return "Draw"
	default:
		return "InProgress"
	}
}

// Match は1回の対戦の進行を管理する。
// 両者が提出するまで盤面には何も反映されないため、同時進行が守られる。
type Match struct {
	cfg     Config
	board   *Board
	turn    int
	pending [4]*Circle // 添字は Player
	hist    []Move
}

func NewMatch(cfg Config) *Match {
	return &Match{cfg: cfg, board: NewBoard(cfg)}
}

func (m *Match) Config() Config  { return m.cfg }
func (m *Match) Turn() int       { return m.turn }
func (m *Match) Board() *Board   { return m.board }
func (m *Match) History() []Move { return m.hist }
func (m *Match) Finished() bool  { return m.turn >= m.cfg.Turns }

// Submit はこのターンの円を提出する。円は自動的に正規化される。
func (m *Match) Submit(p Player, c Circle) error {
	if m.Finished() {
		return ErrMatchFinished
	}
	if p != P1 && p != P2 {
		return ErrInvalidPlayer
	}
	if m.pending[p] != nil {
		return ErrAlreadySubmitted
	}
	cc := m.cfg.ClampCircle(c)
	m.pending[p] = &cc
	return nil
}

// Ready は両者が提出済みかどうかを返す。
func (m *Match) Ready() bool { return m.pending[P1] != nil && m.pending[P2] != nil }

// Resolve は両者の円を盤面に反映してターンを進める。
// 戻り値は [P1の円, P2の円] で、リビール演出に使う。
func (m *Match) Resolve() ([2]Circle, bool) {
	if m.Finished() || !m.Ready() {
		return [2]Circle{}, false
	}
	ink := m.cfg.InkForTurn(m.turn)
	moves := [2]Circle{*m.pending[P1], *m.pending[P2]}
	for i, p := range []Player{P1, P2} {
		m.board.Apply(moves[i], p, ink)
		m.hist = append(m.hist, Move{Circle: moves[i], Player: p, Ink: ink})
	}
	m.pending[P1], m.pending[P2] = nil, nil
	m.turn++
	return moves, true
}

// Result は勝敗を返す。DensityWeight を反映するため Weighted で比較する。
func (m *Match) Result() Result {
	if !m.Finished() {
		return InProgress
	}
	s := m.board.Score()
	switch {
	case s.Weighted[P1] > s.Weighted[P2]:
		return WinP1
	case s.Weighted[P2] > s.Weighted[P1]:
		return WinP2
	default:
		return Draw
	}
}
