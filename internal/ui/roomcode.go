package ui

import (
	"strings"
	"unicode"

	"github.com/morststs/inkmaru-web/internal/signaling"
)

// FormatRoomCode は部屋番号を読み上げやすい形に整える。
// 番号は口頭やチャットで一方が他方に伝えるものなので、3桁ずつ区切って読み違いを減らす。
func FormatRoomCode(code string) string {
	if len(code) <= 3 {
		return code
	}
	return code[:3] + "-" + code[3:]
}

// RoomCodeEntry は部屋番号の入力状態。
//
// スマートフォンに物理キーボードは無い前提なので、画面上のキーパッドから
// 1文字ずつ受け取る。状態機械をここに置くのは、画面を持たない環境でも
// 「入れられない文字が入らないこと」を go test で確かめられるようにするためである。
//
// **受け付ける文字と桁数は internal/signaling から引く。** ここで独自に定義すると、
// サーバ側の定義と食い違ったときに「入力はできるのに必ず参加に失敗する」という
// 気づきにくい壊れ方をする。internal/signaling は標準ライブラリしか import
// しないので、web/game.wasm に入れて差し支えない。
//
// ゼロ値が空の入力欄として使える。
type RoomCodeEntry struct {
	// code は今までに受け取った文字。CodeAlphabet の文字しか入らないため
	// 必ず ASCII であり、末尾1バイトを落とすだけで1文字消せる。
	code string
}

// Append は1文字受け取る。
// 小文字は大文字に直す。CodeAlphabet に無い文字と、既に CodeLength 文字ある
// ときの入力は黙って捨てる（押せてしまうキーがあっても壊れないようにする）。
func (e *RoomCodeEntry) Append(r rune) {
	if len(e.code) >= signaling.CodeLength {
		return
	}
	r = unicode.ToUpper(r)
	if !strings.ContainsRune(signaling.CodeAlphabet, r) {
		return
	}
	e.code += string(r)
}

// Backspace は末尾の1文字を消す。空でも安全である。
func (e *RoomCodeEntry) Backspace() {
	if e.code == "" {
		return
	}
	e.code = e.code[:len(e.code)-1]
}

// Code は今までに入力された文字列を返す。
func (e *RoomCodeEntry) Code() string { return e.code }

// Complete は参加を試してよいかを返す。
// 桁数を自分で数えず signaling.IsValidCode に判断させるのは、
// 「入力欄が受け付ける番号」と「サーバが受け付ける番号」を1つの定義に保つためである。
func (e *RoomCodeEntry) Complete() bool { return signaling.IsValidCode(e.code) }

// Display は入力欄に出す文字列。未入力の桁を "_" で埋め、表示と同じ
// 3桁ずつの区切りを入れる。区切りが入力中と完成後で変わると、
// 番号を読み上げている最中に桁を見失う。
func (e *RoomCodeEntry) Display() string {
	code := e.code
	if n := signaling.CodeLength - len(code); n > 0 {
		code += strings.Repeat("_", n)
	}
	return FormatRoomCode(code)
}
