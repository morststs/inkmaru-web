package ai

import (
	"math/rand"

	"github.com/morststs/inkmaru-web/internal/rules"
)

// Difficulty は CPU の強さ。
type Difficulty int

const (
	Easy Difficulty = iota
	Normal
	Hard
)

func (d Difficulty) String() string {
	switch d {
	case Normal:
		return "ふつう"
	case Hard:
		return "つよい"
	default:
		return "やさしい"
	}
}

// Chooser は CPU の思考。
//
// 引数 hist は「確定して公開された手」だけを含む。プレイヤーがまだ確定して
// いない円をここに渡してはならない。このゲームは同時進行であり、CPU が
// 相手の未確定の手を覗けると成立しないためである（仕様書 §3.6）。
type Chooser interface {
	Choose(hist []rules.Move, cfg rules.Config, me rules.Player, turn int, rnd *rand.Rand) rules.Circle
}

// New は難易度に応じた Chooser を返す。
func New(d Difficulty) Chooser {
	switch d {
	case Normal:
		return &greedy{positions: 12, radii: 5, lookahead: false}
	case Hard:
		return &greedy{positions: 12, radii: 5, lookahead: true}
	default:
		return &greedy{positions: 3, radii: 3, lookahead: false, jitter: 0.5}
	}
}

// FixedRadius は常に同じ半径で最善位置を選ぶ Chooser。
// バランス検証用であり、ゲーム本体では使わない。
func FixedRadius(r float64) Chooser {
	return &greedy{positions: 12, radii: 1, fixedR: r}
}

// playMatchInternal は2つの Chooser を戦わせ、決着した match を返す。
// PlayMatch と PlayMatchBoard が共有する。
func playMatchInternal(cfg rules.Config, p1, p2 Chooser, seed int64) *rules.Match {
	m := rules.NewMatch(cfg)
	rnd := rand.New(rand.NewSource(seed))
	for !m.Finished() {
		turn := m.Turn()
		hist := m.History()
		// 両者とも同じ履歴（＝前ターンまでに公開された盤面）だけを見る。
		c1 := p1.Choose(hist, cfg, rules.P1, turn, rnd)
		c2 := p2.Choose(hist, cfg, rules.P2, turn, rnd)
		m.Submit(rules.P1, c1)
		m.Submit(rules.P2, c2)
		m.Resolve()
	}
	return m
}

// PlayMatch は2つの Chooser を戦わせて結果を返す。
func PlayMatch(cfg rules.Config, p1, p2 Chooser, seed int64) rules.Result {
	return playMatchInternal(cfg, p1, p2, seed).Result()
}

// PlayMatchBoard は2つの Chooser を戦わせ、決着した盤面を返す。
// 陣地の差を測りたいバランステストが使う。
func PlayMatchBoard(cfg rules.Config, p1, p2 Chooser, seed int64) *rules.Board {
	return playMatchInternal(cfg, p1, p2, seed).Board()
}
