package ui

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// pion 由来の長い英文の見本。接続失敗の理由にはこの手の文字列が混ざりうる。
const pionLikeReason = "接続できませんでした (setRemoteDescription failed: sdp: invalid syntax at line 3: unknown attribute ice-ufrag)"

// TestWrapTextRespectsColumns は、どの行も cols 文字を超えないことを守る。
// 超えると画面の端からはみ出して読めなくなる。
func TestWrapTextRespectsColumns(t *testing.T) {
	inputs := []string{
		pionLikeReason,
		"相手が対戦から離れました",
		"short",
		"",
		strings.Repeat("あ", 200),
		"a  b   c",
	}
	for _, in := range inputs {
		for _, cols := range []int{1, 3, 8, 30} {
			for _, line := range WrapText(in, cols, 0) {
				if n := len([]rune(line)); n > cols {
					t.Errorf("cols=%d: 行が %d 文字 (%q) 入力 %q", cols, n, line, in)
				}
			}
		}
	}
}

// TestWrapTextKeepsRunesIntact は多バイト文字を壊さないことを守る。
// バイト単位で切ると、日本語の理由文が壊れた UTF-8 のまま描画側に渡る。
func TestWrapTextKeepsRunesIntact(t *testing.T) {
	const in = "相手のデータが不正でした（board-mismatch）"
	lines := WrapText(in, 5, 0)
	for _, line := range lines {
		if !utf8.ValidString(line) {
			t.Errorf("壊れた UTF-8: %q", line)
		}
	}
	if got := strings.Join(lines, ""); got != in {
		t.Errorf("連結すると %q, 期待 %q", got, in)
	}
	if len(lines) < 2 {
		t.Errorf("折り返されていない: %q", lines)
	}
}

// TestWrapTextBreaksAtSpaces は英文を単語の途中で切らないことを守る。
func TestWrapTextBreaksAtSpaces(t *testing.T) {
	lines := WrapText("unknown attribute ice-ufrag", 12, 0)
	for _, line := range lines {
		if strings.HasPrefix(line, " ") || strings.HasSuffix(line, " ") {
			t.Errorf("行の端に空白が残っている: %q", line)
		}
	}
	if lines[0] != "unknown" {
		t.Errorf("1行目 = %q, 期待 %q", lines[0], "unknown")
	}
	if strings.Join(lines, " ") != "unknown attribute ice-ufrag" {
		t.Errorf("語が失われた: %q", lines)
	}
}

// TestWrapTextTruncatesToMaxLines は行数の上限を守り、
// 切り詰めたことが分かる印を残すことを守る。
// 上限が効かないと、長い理由文がボタンや盤面の上まで書き潰す。
func TestWrapTextTruncatesToMaxLines(t *testing.T) {
	const cols, maxLines = 20, 3
	lines := WrapText(pionLikeReason, cols, maxLines)
	if len(lines) != maxLines {
		t.Fatalf("行数 = %d, 期待 %d (%q)", len(lines), maxLines, lines)
	}
	for _, line := range lines {
		if n := len([]rune(line)); n > cols {
			t.Errorf("切り詰めた行が %d 文字 (%q)", n, line)
		}
	}
	if !strings.HasSuffix(lines[maxLines-1], Ellipsis) {
		t.Errorf("末尾に省略の印が無い: %q", lines[maxLines-1])
	}
	// 収まる文字列には印を付けないこと。
	short := WrapText("接続中", cols, maxLines)
	if len(short) != 1 || short[0] != "接続中" {
		t.Errorf("収まる文字列を触った: %q", short)
	}
}

// TestWrapTextTerminatesOnDegenerateColumns は cols が 0 以下でも
// 無限ループにならないことを守る（画面が極端に狭いときに渡りうる）。
func TestWrapTextTerminatesOnDegenerateColumns(t *testing.T) {
	for _, cols := range []int{-5, 0, 1} {
		lines := WrapText("abc あいう", cols, 0)
		if len(lines) == 0 {
			t.Errorf("cols=%d: 行が返らない", cols)
		}
	}
}

// TestWrapTextTruncatedLineFitsColumns は、**切り詰めた行も cols を超えない**
// ことを守る。
//
// これは TestWrapTextTruncatesToMaxLines とは別のテストである。あちらが選んだ
// cols=20/maxLines=3 では最終行が 14 文字にしかならず、切り詰めの枝を
// 一度も踏まない。だから `runeLen(last) >= cols` を `> cols` に変えても
// 通ってしまう（そのとき最終行は cols+1 文字になる）。
// WrapText を ui に置いた唯一の目的が「画面幅に収める」ことなので、
// 切り詰めた行だけが1文字はみ出すのは、目的そのものを外している。
func TestWrapTextTruncatedLineFitsColumns(t *testing.T) {
	inputs := []string{
		pionLikeReason,
		"理由: 相手のデータが不正でした（board-mismatch）",
		strings.Repeat("あ", 200),
		strings.Repeat("ab ", 60),
	}
	for _, in := range inputs {
		for _, maxLines := range []int{1, 2, 3, 4} {
			for _, cols := range []int{8, 12, 20, 30} {
				lines := WrapText(in, cols, maxLines)
				if len(lines) > maxLines {
					t.Errorf("cols=%d max=%d: %d 行", cols, maxLines, len(lines))
				}
				for _, line := range lines {
					if n := len([]rune(line)); n > cols {
						t.Errorf("cols=%d max=%d: 行が %d 文字 (%q)", cols, maxLines, n, line)
					}
				}
			}
		}
	}
}
