package ai

import (
	"math"
	"math/rand"

	"github.com/morststs/inkmaru-web/internal/rules"
)

// evalGridN は探索に使う盤面の解像度。
// 本番の 256 で探索すると WASM 上で重すぎるため粗くする。
const evalGridN = 96

type greedy struct {
	positions int     // 候補中心を positions×positions の格子で取る
	radii     int     // 候補半径の個数
	lookahead bool    // 相手の最善応手まで読むか
	jitter    float64 // 評価値に混ぜる雑音の割合。Easy を弱くするために使う
	fixedR    float64 // 0 でなければこの半径に固定する
}

func (g *greedy) candidateRadii(cfg rules.Config) []float64 {
	if g.fixedR > 0 {
		return []float64{g.fixedR}
	}
	hi := cfg.MaxRadius()
	if g.radii == 1 {
		return []float64{hi}
	}
	out := make([]float64, g.radii)
	for i := range out {
		// 面積を等間隔に取ると半径の刻みが自然になる。
		t := float64(i) / float64(g.radii-1)
		area := cfg.MinArea + (cfg.MaxArea-cfg.MinArea)*t
		out[i] = math.Sqrt(area / math.Pi)
	}
	return out
}

func (g *greedy) Choose(hist []rules.Move, cfg rules.Config, me rules.Player, turn int, rnd *rand.Rand) rules.Circle {
	eval := cfg
	eval.GridN = evalGridN
	base := rules.BuildBoard(eval, hist)
	ink := cfg.InkForTurn(turn)
	opp := rules.P1
	if me == rules.P1 {
		opp = rules.P2
	}

	best := rules.Circle{CX: 0.5, CY: 0.5, R: cfg.MaxRadius()}
	bestVal := math.Inf(-1)

	for _, r := range g.candidateRadii(cfg) {
		for iy := 0; iy < g.positions; iy++ {
			for ix := 0; ix < g.positions; ix++ {
				c := rules.Circle{
					CX: (float64(ix) + 0.5) / float64(g.positions),
					CY: (float64(iy) + 0.5) / float64(g.positions),
					R:  r,
				}
				b := base.Clone()
				b.Apply(c, me, ink)
				v := diff(b.Score(), me, opp)
				if g.jitter > 0 {
					v *= 1 + g.jitter*(rnd.Float64()*2-1)
				}
				if v > bestVal {
					bestVal, best = v, c
				}
			}
		}
	}

	if g.lookahead && turn < cfg.Turns-1 {
		best = g.refineWithReply(base, cfg, me, opp, turn, best)
	}
	return cfg.ClampCircle(best)
}

// refineWithReply は上位候補について相手の最善応手まで読み、
// 応手を受けたあとの得失差が最大になる手を選び直す。
func (g *greedy) refineWithReply(base *rules.Board, cfg rules.Config, me, opp rules.Player, turn int, fallback rules.Circle) rules.Circle {
	ink := cfg.InkForTurn(turn)
	replyInk := cfg.InkForTurn(turn + 1)
	radii := g.candidateRadii(cfg)

	// 自分の候補を粗く絞る（coarse×coarse = 36点×半径）。
	type cand struct {
		c rules.Circle
		v float64
	}
	var cands []cand
	const coarse = 6
	for _, r := range radii {
		for iy := 0; iy < coarse; iy++ {
			for ix := 0; ix < coarse; ix++ {
				c := rules.Circle{
					CX: (float64(ix) + 0.5) / coarse,
					CY: (float64(iy) + 0.5) / coarse,
					R:  r,
				}
				b := base.Clone()
				b.Apply(c, me, ink)
				cands = append(cands, cand{c, diff(b.Score(), me, opp)})
			}
		}
	}
	// 上位8件だけ深掘りする。
	for i := 0; i < len(cands); i++ {
		for j := i + 1; j < len(cands); j++ {
			if cands[j].v > cands[i].v {
				cands[i], cands[j] = cands[j], cands[i]
			}
		}
	}
	if len(cands) > 8 {
		cands = cands[:8]
	}

	best, bestVal := fallback, math.Inf(-1)
	for _, cd := range cands {
		mine := base.Clone()
		mine.Apply(cd.c, me, ink)
		// 相手の最善応手を探し、その後の得失差で評価する。
		worst := math.Inf(1)
		for _, r := range radii {
			for iy := 0; iy < coarse; iy++ {
				for ix := 0; ix < coarse; ix++ {
					rc := rules.Circle{
						CX: (float64(ix) + 0.5) / coarse,
						CY: (float64(iy) + 0.5) / coarse,
						R:  r,
					}
					b := mine.Clone()
					b.Apply(rc, opp, replyInk)
					if v := diff(b.Score(), me, opp); v < worst {
						worst = v
					}
				}
			}
		}
		if worst > bestVal {
			bestVal, best = worst, cd.c
		}
	}
	return best
}

func diff(s rules.Score, me, opp rules.Player) float64 {
	return s.Weighted[me] - s.Weighted[opp]
}
