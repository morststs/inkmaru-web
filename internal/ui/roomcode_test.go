package ui

import (
	"strings"
	"testing"

	"github.com/morststs/inkmaru-web/internal/signaling"
)

func TestFormatRoomCode(t *testing.T) {
	cases := []struct{ in, want string }{
		{"P2Z5BS", "P2Z-5BS"},
		{"ABCDEF", "ABC-DEF"},
		{"", ""},
		{"ABC", "ABC"},
		{"ABCD", "ABC-D"},
	}
	for _, c := range cases {
		if got := FormatRoomCode(c.in); got != c.want {
			t.Errorf("FormatRoomCode(%q) = %q, 期待 %q", c.in, got, c.want)
		}
	}
}

// TestRoomCodeEntryRejectsRunesOutsideAlphabet は、部屋番号に使えない文字が
// 入力欄に入らないことを守る。入ってしまうと、参加は必ず失敗するのに
// 画面上は6文字揃って見えるという分かりにくい壊れ方をする。
func TestRoomCodeEntryRejectsRunesOutsideAlphabet(t *testing.T) {
	// 紛らわしいので除いてある文字と、キーパッドには無いが取りこぼしたくない記号・多バイト文字。
	for _, r := range []rune{'I', 'O', '0', '1', 'L', '-', ' ', 'あ', '/', 0} {
		var e RoomCodeEntry
		e.Append(r)
		if e.Code() != "" {
			t.Errorf("%q を受け付けてしまった: Code() = %q", r, e.Code())
		}
	}
}

// TestRoomCodeEntryAcceptsEveryAlphabetRune は逆向きの保証。
// 受け付ける文字を絞りすぎて、正当な番号が入力できなくなっていないこと。
func TestRoomCodeEntryAcceptsEveryAlphabetRune(t *testing.T) {
	for _, r := range signaling.CodeAlphabet {
		var e RoomCodeEntry
		e.Append(r)
		if e.Code() != string(r) {
			t.Errorf("%q を受け付けなかった: Code() = %q", r, e.Code())
		}
	}
}

// TestRoomCodeEntryUppercasesLowercase は小文字を大文字に直して受け取ること。
// 直さずに弾くと、外部のキーボード（PCブラウザ）から入力した人が
// 「押しても何も出ない」ことになる。
func TestRoomCodeEntryLowercaseBecomesUppercase(t *testing.T) {
	var e RoomCodeEntry
	for _, r := range "p2z5bs" {
		e.Append(r)
	}
	if e.Code() != "P2Z5BS" {
		t.Errorf("Code() = %q, 期待 %q", e.Code(), "P2Z5BS")
	}
	if !e.Complete() {
		t.Error("6文字揃ったのに Complete() が偽")
	}
}

// TestRoomCodeEntryStopsAtCodeLength は桁数を超えて入らないこと。
// 超えて溜まると、末尾を切って送る側と全部送る側の食い違いになる。
func TestRoomCodeEntryStopsAtCodeLength(t *testing.T) {
	var e RoomCodeEntry
	for i := 0; i < signaling.CodeLength+4; i++ {
		e.Append('A')
	}
	if got := e.Code(); got != strings.Repeat("A", signaling.CodeLength) {
		t.Errorf("Code() = %q (長さ %d), 期待 %d 文字", got, len(got), signaling.CodeLength)
	}
}

// TestRoomCodeEntryCompleteOnlyWhenValid は、揃うまで Complete() が偽であること。
// 真になった瞬間に gfx が join を送るので、ここが早まると必ず失敗する要求を出す。
func TestRoomCodeEntryCompleteOnlyWhenValid(t *testing.T) {
	var e RoomCodeEntry
	for i, r := range "P2Z5BS" {
		if e.Complete() {
			t.Fatalf("%d 文字目を入れる前に Complete() が真 (Code=%q)", i, e.Code())
		}
		e.Append(r)
	}
	if !e.Complete() {
		t.Errorf("6文字揃ったのに Complete() が偽 (Code=%q)", e.Code())
	}
}

// TestRoomCodeEntryBackspace は取り消しの振る舞い。空でも落ちないこと。
func TestRoomCodeEntryBackspace(t *testing.T) {
	var e RoomCodeEntry
	e.Backspace() // 空での取り消し。panic しないこと自体が保証である
	if e.Code() != "" {
		t.Errorf("空で Backspace した結果 Code() = %q", e.Code())
	}
	for _, r := range "P2Z5BS" {
		e.Append(r)
	}
	e.Backspace()
	if e.Code() != "P2Z5B" {
		t.Errorf("Backspace 後の Code() = %q, 期待 %q", e.Code(), "P2Z5B")
	}
	if e.Complete() {
		t.Error("1文字消したのに Complete() が真")
	}
	// 消したぶんはまた入れられること（上限の数え方が壊れていないこと）。
	e.Append('S')
	if e.Code() != "P2Z5BS" || !e.Complete() {
		t.Errorf("消してから入れ直した結果 Code() = %q, Complete() = %v", e.Code(), e.Complete())
	}
}

// TestRoomCodeEntryDisplay は入力欄の見た目。
// 桁の区切りが入力中も完成後も同じ位置であること。
func TestRoomCodeEntryDisplay(t *testing.T) {
	var e RoomCodeEntry
	if got := e.Display(); got != "___-___" {
		t.Errorf("空の Display() = %q, 期待 %q", got, "___-___")
	}
	for _, r := range "P2Z" {
		e.Append(r)
	}
	if got := e.Display(); got != "P2Z-___" {
		t.Errorf("3文字の Display() = %q, 期待 %q", got, "P2Z-___")
	}
	for _, r := range "5BS" {
		e.Append(r)
	}
	if got := e.Display(); got != "P2Z-5BS" {
		t.Errorf("6文字の Display() = %q, 期待 %q", got, "P2Z-5BS")
	}
	if got, want := e.Display(), FormatRoomCode(e.Code()); got != want {
		t.Errorf("完成後の Display() = %q, FormatRoomCode(Code()) = %q", got, want)
	}
}
