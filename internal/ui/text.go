package ui

import "strings"

// Ellipsis は切り詰めた末尾に付ける記号。
const Ellipsis = "…"

// WrapText は文字列を1行 cols 文字までに折り返し、多くとも maxLines 行で返す。
//
// **接続の失敗理由をそのまま画面に出すために要る。** 理由には pion 由来の
// 長い英文が混ざりうるので、1行に収まる前提で書くことはできない。
// maxLines が 0 以下なら行数を制限しない。
//
// 文字単位（rune）で数えるのが要点である。バイト単位で切ると、日本語の
// 理由文を切ったときに壊れた UTF-8 が描画側に渡る。
//
// 空白があればそこで折る（英文が単語の途中で切れないように）。空白の無い
// 日本語はそのまま cols 文字で折る。
func WrapText(s string, cols, maxLines int) []string {
	if cols < 1 {
		cols = 1
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		out = append(out, wrapParagraph(para, cols)...)
	}
	if maxLines > 0 && len(out) > maxLines {
		out = out[:maxLines]
		last := out[maxLines-1]
		if runeLen(last) >= cols {
			last = truncRunes(last, cols-1)
		}
		out[maxLines-1] = last + Ellipsis
	}
	return out
}

func wrapParagraph(s string, cols int) []string {
	rs := []rune(s)
	if len(rs) == 0 {
		return []string{""}
	}
	var out []string
	for len(rs) > cols {
		// 折る位置は既定で cols。行内に空白があればその手前まで戻す。
		cut := cols
		for i := cols; i > 0; i-- {
			if rs[i] == ' ' {
				cut = i
				break
			}
		}
		out = append(out, strings.TrimRight(string(rs[:cut]), " "))
		rs = rs[cut:]
		for len(rs) > 0 && rs[0] == ' ' {
			rs = rs[1:]
		}
	}
	if len(rs) == 0 && len(out) > 0 {
		// 末尾の空白だけが残った。空行を足さない。
		return out
	}
	return append(out, string(rs))
}

func runeLen(s string) int { return len([]rune(s)) }

func truncRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n])
}
