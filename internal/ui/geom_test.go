package ui

import (
	"image"
	"strings"
	"testing"

	"github.com/morststs/inkmaru-web/internal/signaling"
)

func TestPointIn(t *testing.T) {
	r := image.Rect(10, 20, 30, 40)
	cases := []struct {
		x, y int
		want bool
	}{
		{10, 20, true},  // 左上は含む
		{29, 39, true},  // 右下の内側
		{30, 40, false}, // 右下の境界は含まない
		{9, 20, false},
		{20, 41, false},
	}
	for _, c := range cases {
		if got := PointIn(r, c.x, c.y); got != c.want {
			t.Errorf("PointIn(%d,%d) = %v, 期待 %v", c.x, c.y, got, c.want)
		}
	}
}

func TestTitleButtonsAreThreeStackedInsideBoard(t *testing.T) {
	l := ComputeLayout(390, 844)
	btns := TitleButtons(l)

	for i, b := range btns {
		if !b.In(l.Board) {
			t.Errorf("ボタン%d が盤面領域からはみ出した %+v (盤面 %+v)", i, b, l.Board)
		}
		if b.Dy() <= 0 {
			t.Errorf("ボタン%d の高さが 0 以下 %+v", i, b)
		}
	}
	for i := 1; i < len(btns); i++ {
		if btns[i].Min.Y < btns[i-1].Max.Y {
			t.Errorf("ボタン%d が ボタン%d と重なっている", i, i-1)
		}
	}
	if btns[0].Dy() != btns[1].Dy() || btns[1].Dy() != btns[2].Dy() {
		t.Error("ボタンの高さが揃っていない")
	}
}

// onlineRects はオンライン画面に同時に出る矩形をすべて集める。
func onlineRects(l Layout) []image.Rectangle {
	var out []image.Rectangle
	for _, b := range OnlineButtons(l, false) {
		out = append(out, b.Rect)
	}
	for _, k := range KeypadKeys(l) {
		out = append(out, k.Rect)
	}
	return out
}

// TestOnlineButtonDoesNotCollideWithTitleButtons は、タイトル画面に足した
// 「オンライン対戦」が難易度の3つと重ならないことを守る。重なると
// どちらのボタンを押したのか分からなくなる（先に判定したほうが常に勝つ）。
func TestOnlineButtonDoesNotCollideWithTitleButtons(t *testing.T) {
	l := ComputeLayout(390, 844)
	ob := TitleOnlineButton(l)
	if ob.Empty() {
		t.Fatalf("オンライン対戦ボタンが空 %+v", ob)
	}
	if !ob.In(l.Screen) {
		t.Errorf("オンライン対戦ボタンが画面からはみ出した %+v (画面 %+v)", ob, l.Screen)
	}
	for i, b := range TitleButtons(l) {
		if ob.Overlaps(b) {
			t.Errorf("オンライン対戦ボタン %+v が難易度ボタン%d %+v と重なっている", ob, i, b)
		}
	}
}

// TestOnlineScreenRectsDoNotOverlap はオンライン画面の矩形が
// 画面内に収まり、互いに重ならないことを守る。
func TestOnlineScreenRectsDoNotOverlap(t *testing.T) {
	l := ComputeLayout(390, 844)
	rects := onlineRects(l)
	for i, r := range rects {
		if r.Empty() {
			t.Errorf("矩形%d が空 %+v", i, r)
		}
		if !r.In(l.Screen) {
			t.Errorf("矩形%d が画面からはみ出した %+v (画面 %+v)", i, r, l.Screen)
		}
	}
	for i := range rects {
		for j := i + 1; j < len(rects); j++ {
			if rects[i].Overlaps(rects[j]) {
				t.Errorf("矩形%d %+v と矩形%d %+v が重なっている", i, rects[i], j, rects[j])
			}
		}
	}
}

// TestKeypadCoversEveryCodeRune はキーパッドが部屋番号の全文字を
// ちょうど1回ずつ持つことを守る。
//
// **1文字でも欠けると、その文字を含む部屋番号には永久に参加できない。**
// しかもその番号を引いたときにしか露見しないので、目視では見つからない。
func TestKeypadCoversEveryCodeRune(t *testing.T) {
	keys := KeypadKeys(ComputeLayout(390, 844))

	count := map[rune]int{}
	for _, k := range keys {
		count[k.Rune]++
	}
	for _, r := range signaling.CodeAlphabet {
		switch count[r] {
		case 1:
		case 0:
			t.Errorf("キーパッドに %q が無い", r)
		default:
			t.Errorf("キーパッドに %q が %d 個ある", r, count[r])
		}
	}
	if count[0] != 1 {
		t.Errorf("取り消しキー（Rune=0）が %d 個, 期待 1", count[0])
	}
	if len(keys) != len(signaling.CodeAlphabet)+1 {
		t.Errorf("キーの数 = %d, 期待 %d", len(keys), len(signaling.CodeAlphabet)+1)
	}

	// 入力欄の状態機械が受け付ける文字と一致していること。
	// ここが食い違うと「押せるのに入らないキー」ができる。
	for _, k := range keys {
		if k.Rune == 0 {
			continue
		}
		var e RoomCodeEntry
		e.Append(k.Rune)
		if e.Code() != string(k.Rune) {
			t.Errorf("キー %q を押しても入力欄に入らない", k.Rune)
		}
	}
}

// rectWithin は「矩形が親の領域の座標の中に収まっているか」を厳密に見る。
//
// image.Rectangle.In を使わないのは、空の矩形に対して常に true を返すからである。
// 潰れた矩形が親の外の座標を指していても In では気づけない。
func rectWithin(r, parent image.Rectangle) bool {
	return r.Min.X >= parent.Min.X && r.Max.X <= parent.Max.X &&
		r.Min.Y >= parent.Min.Y && r.Max.Y <= parent.Max.Y
}

// TestOnlineRectsStayInsideTheirArea は、どの画面サイズでもオンライン画面の
// 矩形が持ち場（キーパッドは盤面領域、ボタンはボタンの帯）から出ないことを守る。
//
// 潰れるほど狭い画面が要点である。幅が足りないときに素直に足していくと、
// 空の矩形が持ち場の外の座標を指し、そこに描く実装だと他の要素の上に重なる。
func TestOnlineRectsStayInsideTheirArea(t *testing.T) {
	for _, sz := range [][2]int{{390, 844}, {200, 320}, {120, 200}, {1, 1}} {
		l := ComputeLayout(sz[0], sz[1])
		for i, b := range OnlineButtons(l, false) {
			if !rectWithin(b.Rect, l.Button) {
				t.Errorf("%dx%d: ボタン%d %+v がボタンの帯 %+v から出た", sz[0], sz[1], i, b.Rect, l.Button)
			}
		}
		for i, k := range KeypadKeys(l) {
			if !rectWithin(k.Rect, l.Board) {
				t.Errorf("%dx%d: キー%d %+v が盤面領域 %+v から出た", sz[0], sz[1], i, k.Rect, l.Board)
			}
		}
		if !rectWithin(TitleOnlineButton(l), l.Screen) {
			t.Errorf("%dx%d: オンライン対戦ボタン %+v が画面から出た", sz[0], sz[1], TitleOnlineButton(l))
		}
	}
}

// TestOnlineRectsSurviveSmallScreen は、小さくても成立する画面（200x320）で
// 矩形が押せる大きさを保ち、互いに重ならないことを守る。
//
// これより狭い 120x200 では ComputeLayout が盤面そのものを 1x1 に潰すので、
// キーが押せる大きさであることは元から成り立たない。そこまで含めて守れる
// のは「持ち場から出ないこと」だけであり、それは別のテストが見ている。
func TestOnlineRectsSurviveSmallScreen(t *testing.T) {
	l := ComputeLayout(200, 320)
	rects := onlineRects(l)
	for i, r := range rects {
		if r.Empty() {
			t.Errorf("矩形%d が潰れて押せない %+v", i, r)
		}
	}
	for i := range rects {
		for j := i + 1; j < len(rects); j++ {
			if rects[i].Overlaps(rects[j]) {
				t.Errorf("矩形%d %+v と矩形%d %+v が重なっている", i, rects[i], j, rects[j])
			}
		}
	}
}

// TestOnlineButtonsPairActionsWithLabels は、ボタンの意味と見出しが
// 1組で決まっていることを守る。
//
// **押下の判定と描画の見出しが別々に添字を解釈すると、片方だけ並べ替えたときに
// 「もどる」を押して部屋が作られる。** しかも gfx にはこの環境でテストが書けない。
// 意味・見出し・矩形を1つの値にまとめ、その対応をここで固定する。
func TestOnlineButtonsPairActionsWithLabels(t *testing.T) {
	btns := OnlineButtons(ComputeLayout(390, 844), false)

	wantActions := [3]OnlineAction{OnlineCreate, OnlineJoin, OnlineBack}
	for i, b := range btns {
		if b.Action != wantActions[i] {
			t.Errorf("ボタン%d の意味 = %v, 期待 %v", i, b.Action, wantActions[i])
		}
	}

	// 見出しは意味ごとに違い、空でないこと。
	seen := map[string]OnlineAction{}
	for _, b := range btns {
		if b.Label == "" {
			t.Errorf("%v の見出しが空", b.Action)
		}
		if prev, dup := seen[b.Label]; dup {
			t.Errorf("見出し %q が %v と %v で重複", b.Label, prev, b.Action)
		}
		seen[b.Label] = b.Action
	}

	// 「もどる」の見出しが「もどる」以外の意味に付いていないこと。
	// ここが入れ替わるのが、この型で防ぎたい壊れ方そのものである。
	for _, b := range btns {
		if b.Label == "もどる" && b.Action != OnlineBack {
			t.Errorf("見出し「もどる」が %v に付いている", b.Action)
		}
		if b.Action == OnlineBack && b.Label != "もどる" {
			t.Errorf("OnlineBack の見出しが %q", b.Label)
		}
		if b.Action == OnlineCreate && !strings.Contains(b.Label, "作") {
			t.Errorf("OnlineCreate の見出しが %q", b.Label)
		}
	}
}

// TestInfoColsIsPositive は、どの画面サイズでも桁数が1以上であることを守る。
// 0 以下を折り返しに渡すと、意味のある行が組み立てられない。
func TestInfoColsIsPositive(t *testing.T) {
	for _, sz := range [][2]int{{390, 844}, {200, 320}, {120, 200}, {1, 1}} {
		l := ComputeLayout(sz[0], sz[1])
		if got := InfoCols(l); got < 1 {
			t.Errorf("%dx%d: InfoCols = %d", sz[0], sz[1], got)
		}
	}
	// 広い画面では狭い画面より多く入ること（幅を見ていない実装を弾く）。
	wide := InfoCols(ComputeLayout(390, 844))
	narrow := InfoCols(ComputeLayout(200, 320))
	if wide <= narrow {
		t.Errorf("390px で %d 桁, 200px で %d 桁（幅を見ていない）", wide, narrow)
	}
}

// 結果画面のボタンは、意味と見出しが1組で動く。
func TestResultButtons(t *testing.T) {
	l := ComputeLayout(390, 844)
	cases := []struct {
		canRematch, asked bool
		want              []ResultButton
	}{
		{false, false, []ResultButton{{Action: ResultQuit, Label: "もう一度"}}},
		{false, true, []ResultButton{{Action: ResultQuit, Label: "もう一度"}}},
		{true, false, []ResultButton{
			{Action: ResultRematch, Label: "もう一度"},
			{Action: ResultQuit, Label: "やめる"},
		}},
		{true, true, []ResultButton{{Action: ResultQuit, Label: "やめる"}}},
	}
	for _, c := range cases {
		got := ResultButtons(l, c.canRematch, c.asked)
		if len(got) != len(c.want) {
			t.Fatalf("canRematch=%v asked=%v: %d個, 期待 %d個", c.canRematch, c.asked, len(got), len(c.want))
		}
		for i := range got {
			if got[i].Action != c.want[i].Action || got[i].Label != c.want[i].Label {
				t.Errorf("canRematch=%v asked=%v の %d番目 = %v %q, 期待 %v %q",
					c.canRematch, c.asked, i, got[i].Action, got[i].Label, c.want[i].Action, c.want[i].Label)
			}
		}
	}
}

// どの矩形も帯の中に収まり、互いに重ならない。
func TestResultButtonsStayInsideTheBand(t *testing.T) {
	for _, size := range [][2]int{{390, 844}, {200, 320}, {1024, 768}} {
		l := ComputeLayout(size[0], size[1])
		for _, asked := range []bool{false, true} {
			bs := ResultButtons(l, true, asked)
			for i, b := range bs {
				if !b.Rect.In(l.Button) {
					t.Errorf("%dx%d asked=%v: %d番目 %v が帯 %v からはみ出した",
						size[0], size[1], asked, i, b.Rect, l.Button)
				}
				if i > 0 && b.Rect.Overlaps(bs[i-1].Rect) {
					t.Errorf("%dx%d asked=%v: %d番目と%d番目が重なった", size[0], size[1], asked, i-1, i)
				}
			}
		}
	}
}
