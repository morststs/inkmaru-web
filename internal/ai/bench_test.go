package ai

import (
	"math/rand"
	"testing"

	"github.com/morststs/inkmaru-web/internal/rules"
)

func BenchmarkChooseNormal(b *testing.B) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 128 // 他のテストと同様、小さいグリッドを使う

	// 中盤の履歴を組み立てる: 確定済み2ターン分（4手）
	hist := []rules.Move{
		{Circle: rules.Circle{CX: 0.25, CY: 0.25, R: cfg.MinRadius()}, Player: rules.P1, Ink: cfg.InkForTurn(0)},
		{Circle: rules.Circle{CX: 0.75, CY: 0.75, R: cfg.MinRadius()}, Player: rules.P2, Ink: cfg.InkForTurn(0)},
		{Circle: rules.Circle{CX: 0.25, CY: 0.75, R: cfg.MinRadius()}, Player: rules.P1, Ink: cfg.InkForTurn(1)},
		{Circle: rules.Circle{CX: 0.75, CY: 0.25, R: cfg.MinRadius()}, Player: rules.P2, Ink: cfg.InkForTurn(1)},
	}

	chooser := New(Normal)
	rnd := rand.New(rand.NewSource(1))
	turn := 2 // 近視的な探索経路を測定（難度による差なし）

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		chooser.Choose(hist, cfg, rules.P1, turn, rnd)
	}
}

func BenchmarkChooseHard(b *testing.B) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 128 // 他のテストと同様、小さいグリッドを使う

	// 中盤の履歴を組み立てる: 確定済み1ターン分（2手）
	hist := []rules.Move{
		{Circle: rules.Circle{CX: 0.25, CY: 0.25, R: cfg.MinRadius()}, Player: rules.P1, Ink: cfg.InkForTurn(0)},
		{Circle: rules.Circle{CX: 0.75, CY: 0.75, R: cfg.MinRadius()}, Player: rules.P2, Ink: cfg.InkForTurn(0)},
	}

	chooser := New(Hard)
	rnd := rand.New(rand.NewSource(1))
	turn := 1 // lookahead は turn < cfg.Turns-1 の時だけ実行。最後のターンを測定すると cheap path になる

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		chooser.Choose(hist, cfg, rules.P1, turn, rnd)
	}
}
