// Package ui は表示ロジックを Ebitengine から切り離して保つパッケージである。
// 画面を持たない CI やコントリビューターの手元でも `go test` だけで検証できるように、
// このパッケージとその依存は Ebitengine を一切 import しない。
// 実際の描画・入力は internal/gfx が担い、ここが提供する値・関数を使って
// Ebitengine の API を呼び出す。
package ui

import (
	"image"

	"github.com/morststs/inkmaru-web/internal/signaling"
)

// PointIn は点が矩形の中にあるかを返す。右端と下端は含まない。
func PointIn(r image.Rectangle, x, y int) bool {
	return x >= r.Min.X && x < r.Max.X && y >= r.Min.Y && y < r.Max.Y
}

// TitleButtons はタイトル画面の難易度ボタン3つの矩形を返す。
// 盤面と同じ領域を縦3等分して使う。
func TitleButtons(l Layout) [3]image.Rectangle {
	area := l.Board
	h := (area.Dy() - 2*gap) / 3
	var out [3]image.Rectangle
	for i := range out {
		y := area.Min.Y + i*(h+gap)
		out[i] = image.Rect(area.Min.X, y, area.Max.X, y+h)
	}
	return out
}

// TitleOnlineButton はタイトル画面の「オンライン対戦」ボタンの矩形を返す。
//
// 難易度の3つは盤面領域を使い切っているので、こちらは l.Button の帯を使う。
// タイトル画面はこの領域を今まで使っていない。
func TitleOnlineButton(l Layout) image.Rectangle { return l.Button }

// OnlineAction はオンライン画面のボタンが起こすこと。
type OnlineAction int

const (
	// OnlineCreate は部屋を作る。
	OnlineCreate OnlineAction = iota
	// OnlineJoin は入力した番号の部屋に参加する。
	OnlineJoin
	// OnlineBack は組み立てをやめてタイトルへ戻る。
	OnlineBack
)

// OnlineButton はオンライン画面のボタン1つ。意味と見出しと矩形を1組で持つ。
//
// **添字と意味の対応を1箇所に閉じ込めることが目的である。** 押下の判定と
// 描画の見出しが別々に添字を解釈していると、片方だけ並べ替えたときに
// 「もどる」を押して部屋が作られる。しかも gfx にはこの環境でテストが書けない。
// ui.ScoreLabels が「色と見出しを1組で返す」のと同じ形にしてある。
type OnlineButton struct {
	Action   OnlineAction
	Label    string
	Emphasis Emphasis
	Rect     image.Rectangle
}

// OnlineButtons はオンライン画面の [部屋を作る] [参加する] [もどる] を返す。
// l.Button の帯を横に3等分する。
//
// connecting は、接続の組み立てを始めたあとかどうか。**true のとき [もどる] 以外は
// EmphasisDisabled になる**（作成中に「参加する」を押せると、捨てられた接続が残る）。
// 描画も押下もこの Emphasis を見る。「押せない」の判断を gfx に置かないためである。
//
// **必ず帯の中に収める。** 幅が3つ分に足りない画面では計算上の幅が負になり、
// 素直に足していくと帯からはみ出した位置にボタンが並ぶ。そこは他の何かが
// 描かれている場所なので、押せない当たり判定が重なることになる。
func OnlineButtons(l Layout, connecting bool) [3]OnlineButton {
	out := [3]OnlineButton{
		{Action: OnlineCreate, Label: "部屋を作る", Emphasis: EmphasisPrimary},
		{Action: OnlineJoin, Label: "参加する", Emphasis: EmphasisPrimary},
		{Action: OnlineBack, Label: "もどる", Emphasis: EmphasisSecondary},
	}
	area := l.Button
	w := (area.Dx() - 2*gap) / 3
	for i := range out {
		if connecting && out[i].Action != OnlineBack {
			out[i].Emphasis = EmphasisDisabled
		}
		x0 := clampInt(area.Min.X+i*(w+gap), area.Min.X, area.Max.X)
		x1 := clampInt(x0+w, x0, area.Max.X)
		out[i].Rect = image.Rect(x0, area.Min.Y, x1, area.Max.Y)
	}
	return out
}

// InfoCols は情報欄に収まる文字数。折り返しの桁数に使う。
//
// **全角を12px として数える。** 画面の文字は 12px の M PLUS Rounded 1c
// （internal/assets）で、半角はおおよそその半分である。半角だけの行は幅が
// 余るが、日本語と英文が混ざった理由文が右端を突き抜けるよりよい。狭い画面でも1以上を返す
// （0 を渡すと WrapText が桁数を1に丸めるだけだが、ここで意味を決めておく）。
func InfoCols(l Layout) int {
	cols := l.Info.Dx() / 12
	if cols < 1 {
		cols = 1
	}
	return cols
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// KeypadKey はキーパッドのキー1つ。Rune が 0 のキーは1文字消す取り消しキーで、
// 描画側が「←」と描く。
type KeypadKey struct {
	Rune rune
	Rect image.Rectangle
}

// キーパッドの格子。signaling.CodeAlphabet の31文字に取り消しキーを足した32個が
// 8×4 にちょうど収まる。列数を変えるなら余りの扱いを考え直すこと。
const (
	keypadCols = 8
	keypadRows = 4
	keyPad     = 2
)

// KeypadKeys は部屋番号を入力するキーパッドの矩形を返す。
//
// **signaling.CodeAlphabet の全文字を必ず1回ずつ含める。** 1文字でも欠けると、
// その文字を含む部屋番号には永久に参加できない（しかも欠けた文字を含む番号を
// 引いたときにしか露見しない）。だから列数・行数を決め打ちで並べるのではなく、
// アルファベットを端から並べて余りに取り消しキーを置く形にしてある。
//
// 盤面領域の中に正方形のキーを敷き、余白は上下左右に均等に分ける。
// 極端に狭い画面ではキーが潰れて大きさ 0 になるが、負にはならない。
func KeypadKeys(l Layout) []KeypadKey {
	area := l.Board
	side := area.Dx() / keypadCols
	if h := area.Dy() / keypadRows; h < side {
		side = h
	}
	if side < 0 {
		side = 0
	}
	ox := area.Min.X + (area.Dx()-keypadCols*side)/2
	oy := area.Min.Y + (area.Dy()-keypadRows*side)/2

	runes := make([]rune, 0, len(signaling.CodeAlphabet)+1)
	for _, r := range signaling.CodeAlphabet {
		runes = append(runes, r)
	}
	runes = append(runes, 0) // 取り消し

	out := make([]KeypadKey, 0, len(runes))
	for i, r := range runes {
		col, row := i%keypadCols, i/keypadCols
		if row >= keypadRows {
			break // 格子に入り切らない分は捨てる（現状の32個では起きない）
		}
		x := ox + col*side
		y := oy + row*side
		out = append(out, KeypadKey{Rune: r, Rect: insetRect(image.Rect(x, y, x+side, y+side), keyPad)})
	}
	return out
}

// insetRect は矩形を四辺から pad だけ縮める。
// 縮めると潰れてしまう小さな矩形はそのまま返す（負の大きさを作らない）。
func insetRect(r image.Rectangle, pad int) image.Rectangle {
	if r.Dx() <= 2*pad || r.Dy() <= 2*pad {
		return r
	}
	return image.Rect(r.Min.X+pad, r.Min.Y+pad, r.Max.X-pad, r.Max.Y-pad)
}

// ResultAction は結果画面のボタンの意味。
type ResultAction int

const (
	// ResultRematch は同じ相手にもう一度申し出る。
	ResultRematch ResultAction = iota
	// ResultQuit は対戦の持ち物を手放してタイトルへ戻る。
	ResultQuit
)

// ResultButton は結果画面のボタン1つ。意味と見出しと矩形を1組で持つ。
//
// **添字と意味の対応を1箇所に閉じ込めることが目的である**（OnlineButton と同じ）。
// 押下の判定と描画の見出しが別々に添字を解釈していると、片方だけ並べ替えたときに
// 「やめる」を押して再戦を申し出ることになる。gfx にはテストが書けない。
type ResultButton struct {
	Action   ResultAction
	Label    string
	Emphasis Emphasis
	Rect     image.Rectangle
}

// ResultButtons は結果画面のボタンを返す。
//
// 再戦できないとき（CPU 対戦、中断で終わった対戦、相手が離脱した対戦）は
// 今までどおり1つだけで、押すとタイトルへ戻る。見出しも今までのままにする。
//
// 申し出たあとは [やめる] だけを残す。二度押しは Session が畳むので害は無いが、
// 押せるボタンが残っていると「まだ伝わっていないのでは」と読める。
func ResultButtons(l Layout, canRematch, asked bool) []ResultButton {
	var out []ResultButton
	switch {
	case !canRematch:
		// 再戦できないときは、これが唯一のボタンなので主にする。
		out = []ResultButton{{Action: ResultQuit, Label: "もう一度", Emphasis: EmphasisPrimary}}
	case asked:
		// 相手の返事を待っている。やめる以外にできることが無いが、
		// 待つことのほうが主なので副に留める。
		out = []ResultButton{{Action: ResultQuit, Label: "やめる", Emphasis: EmphasisSecondary}}
	default:
		out = []ResultButton{
			{Action: ResultRematch, Label: "もう一度", Emphasis: EmphasisPrimary},
			{Action: ResultQuit, Label: "やめる", Emphasis: EmphasisSecondary},
		}
	}
	area := l.Button
	n := len(out)
	w := (area.Dx() - (n-1)*gap) / n
	for i := range out {
		x0 := clampInt(area.Min.X+i*(w+gap), area.Min.X, area.Max.X)
		x1 := clampInt(x0+w, x0, area.Max.X)
		out[i].Rect = image.Rect(x0, area.Min.Y, x1, area.Max.Y)
	}
	return out
}
