package ui

import (
	"testing"

	"github.com/morststs/inkmaru-web/internal/rules"
)

// 通算成績は席から数える。
//
// **席を見ずに WinP1 を「勝ち」と数えると、ゲスト（席=P2）の通算成績は
// 勝敗が丸ごと逆になる。** 1戦ごとの勝敗の文言は ui.ResultMessage が席を見て
// 決めているので、画面には「あなたの勝ち」と出たまま通算だけが1敗増える。
func TestSeriesCountsFromSeat(t *testing.T) {
	cases := []struct {
		name         string
		seat         rules.Player
		results      []rules.Result
		wins, losses int
		draws        int
	}{
		{"ホストが勝つ", rules.P1, []rules.Result{rules.WinP1}, 1, 0, 0},
		{"ホストが負ける", rules.P1, []rules.Result{rules.WinP2}, 0, 1, 0},
		{"ゲストが勝つ", rules.P2, []rules.Result{rules.WinP2}, 1, 0, 0},
		{"ゲストが負ける", rules.P2, []rules.Result{rules.WinP1}, 0, 1, 0},
		{"引き分け", rules.P1, []rules.Result{rules.Draw}, 0, 0, 1},
		{"続けて数える", rules.P2, []rules.Result{rules.WinP2, rules.WinP1, rules.Draw, rules.WinP2}, 2, 1, 1},
	}
	for _, c := range cases {
		var s Series
		for _, r := range c.results {
			s.Record(r, c.seat)
		}
		if s.Wins != c.wins || s.Losses != c.losses || s.Draws != c.draws {
			t.Errorf("%s: %d勝 %d敗 %d分, 期待 %d勝 %d敗 %d分",
				c.name, s.Wins, s.Losses, s.Draws, c.wins, c.losses, c.draws)
		}
	}
}

// 決着していない対戦（中断など）は数えない。
func TestSeriesIgnoresUnfinishedMatch(t *testing.T) {
	var s Series
	s.Record(rules.InProgress, rules.P1)
	if s.Total() != 0 {
		t.Errorf("決着していない対戦を数えた: %d勝 %d敗 %d分", s.Wins, s.Losses, s.Draws)
	}
}

// 通算の一言は2戦目から出す。1戦目で「通算 1勝」と出しても情報が無い。
func TestSeriesLine(t *testing.T) {
	cases := []struct {
		w, l, d int
		want    string
	}{
		{0, 0, 0, ""},
		{1, 0, 0, ""},
		{2, 0, 0, "通算 2勝"},
		{2, 1, 0, "通算 2勝 1敗"},
		{0, 2, 0, "通算 2敗"},
		{1, 1, 0, "通算 1勝 1敗"},
		{1, 1, 1, "通算 1勝 1敗 1分"},
		{0, 0, 2, "通算 2分"},
	}
	for _, c := range cases {
		s := Series{Wins: c.w, Losses: c.l, Draws: c.d}
		if got := s.Line(); got != c.want {
			t.Errorf("Series{%d,%d,%d}.Line() = %q, 期待 %q", c.w, c.l, c.d, got, c.want)
		}
	}
}
