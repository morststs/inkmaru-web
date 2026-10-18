package ui

import (
	"strconv"
	"strings"

	"github.com/morststs/inkmaru-web/internal/rules"
)

// Series は同じ相手と続けた通算成績。
//
// 接続が生きているあいだだけの値で、保存はしない。相手が変われば別の通算になる。
//
// 数え方を internal/ui に置いているのは、席から見た勝敗という**判断**だからである。
// internal/gfx はこの開発環境ではテストが書けないので、そこに置いた判断は誰にも
// 検証されないまま出荷される。
type Series struct {
	Wins   int
	Losses int
	Draws  int
}

// Record は1戦の結果を席から見て数える。
//
// **席を見ずに rules.WinP1 を「勝ち」と数えてはならない。** ゲストの席は P2 なので、
// 通算だけが勝敗の逆になり、1戦ごとの文言（ResultMessage は席を見る）と食い違う。
//
// 決着していない結果（中断で終わった対戦）は数えない。
func (s *Series) Record(res rules.Result, seat rules.Player) {
	switch res {
	case rules.Draw:
		s.Draws++
	case rules.WinP1:
		if seat == rules.P1 {
			s.Wins++
		} else {
			s.Losses++
		}
	case rules.WinP2:
		if seat == rules.P2 {
			s.Wins++
		} else {
			s.Losses++
		}
	}
}

// Total は数えた対戦数。
func (s Series) Total() int { return s.Wins + s.Losses + s.Draws }

// Line は結果画面に出す一言。2戦目から出す。
//
// 1戦目に「通算 1勝」と出しても、すぐ上に出ている1戦の勝敗と同じことしか言わない。
// 空文字を返すので、画面側は中身を見ずにそのまま描けばよい（判断を gfx に置かない）。
func (s Series) Line() string {
	if s.Total() < 2 {
		return ""
	}
	var parts []string
	if s.Wins > 0 {
		parts = append(parts, strconv.Itoa(s.Wins)+"勝")
	}
	if s.Losses > 0 {
		parts = append(parts, strconv.Itoa(s.Losses)+"敗")
	}
	if s.Draws > 0 {
		parts = append(parts, strconv.Itoa(s.Draws)+"分")
	}
	return "通算 " + strings.Join(parts, " ")
}
