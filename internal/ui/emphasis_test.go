package ui

import (
	"image"
	"testing"
)

// ボタンの強調度は意味から決まる。
//
// **gfx に判断を置いてはならない。** かつて gfx は「ResultQuit かつ再戦できる
// なら別の色」と添字と意味を解釈して塗り分けていた。この開発環境では gfx に
// テストが1行も書けないので、主副が入れ替わっても誰も気づかない。
func TestResultButtonEmphasis(t *testing.T) {
	l := ComputeLayout(390, 844)
	cases := []struct {
		name              string
		canRematch, asked bool
		want              map[ResultAction]Emphasis
	}{
		{
			name: "再戦できない（CPU対戦）",
			want: map[ResultAction]Emphasis{ResultQuit: EmphasisPrimary},
		},
		{
			name: "再戦できる", canRematch: true,
			want: map[ResultAction]Emphasis{
				ResultRematch: EmphasisPrimary,
				ResultQuit:    EmphasisSecondary,
			},
		},
		{
			name: "申し出たあと", canRematch: true, asked: true,
			want: map[ResultAction]Emphasis{ResultQuit: EmphasisSecondary},
		},
	}
	for _, c := range cases {
		got := ResultButtons(l, c.canRematch, c.asked)
		if len(got) != len(c.want) {
			t.Errorf("%s: ボタン数 = %d, 期待 %d", c.name, len(got), len(c.want))
			continue
		}
		for _, b := range got {
			want, ok := c.want[b.Action]
			if !ok {
				t.Errorf("%s: 想定外のボタン %v", c.name, b.Action)
				continue
			}
			if b.Emphasis != want {
				t.Errorf("%s: %v の強調度 = %v, 期待 %v", c.name, b.Action, b.Emphasis, want)
			}
		}
	}
}

// オンライン画面では「部屋を作る」と「参加する」が主、「もどる」が副である。
func TestOnlineButtonEmphasis(t *testing.T) {
	l := ComputeLayout(390, 844)
	want := map[OnlineAction]Emphasis{
		OnlineCreate: EmphasisPrimary,
		OnlineJoin:   EmphasisPrimary,
		OnlineBack:   EmphasisSecondary,
	}
	got := OnlineButtons(l, false)
	if len(got) != len(want) {
		t.Fatalf("ボタン数 = %d, 期待 %d", len(got), len(want))
	}
	for _, b := range got {
		w, ok := want[b.Action]
		if !ok {
			t.Errorf("想定外のボタン %v", b.Action)
			continue
		}
		if b.Emphasis != w {
			t.Errorf("%v の強調度 = %v, 期待 %v", b.Action, b.Emphasis, w)
		}
	}
}

// 設定し忘れたボタンが主として塗られてはならない。ゼロ値は押せない扱いにする。
func TestEmphasisZeroValueIsNotPrimary(t *testing.T) {
	var e Emphasis
	if e != EmphasisDisabled {
		t.Errorf("ゼロ値 = %v, 期待 EmphasisDisabled", e)
	}
}

// 強調度はボタンの矩形に影響しない。見た目の重みと当たり判定は別である。
//
// 値は強調度を足す直前の実装（9550b6f）が 390x844 で返した矩形をそのまま
// 固定している。「画面内にある」だけでは1pxのずれを捕まえられない。
func TestEmphasisDoesNotMoveRects(t *testing.T) {
	l := ComputeLayout(390, 844)

	online := OnlineButtons(l, false)
	wantOnline := map[OnlineAction]image.Rectangle{
		OnlineCreate: image.Rect(12, 784, 128, 844),
		OnlineJoin:   image.Rect(136, 784, 252, 844),
		OnlineBack:   image.Rect(260, 784, 376, 844),
	}
	if len(online) != len(wantOnline) {
		t.Fatalf("オンラインのボタン数 = %d, 期待 %d", len(online), len(wantOnline))
	}
	for _, b := range online {
		if b.Rect != wantOnline[b.Action] {
			t.Errorf("オンライン %v の矩形 = %v, 期待 %v", b.Action, b.Rect, wantOnline[b.Action])
		}
	}

	cases := []struct {
		name              string
		canRematch, asked bool
		want              map[ResultAction]image.Rectangle
	}{
		{"再戦できない", false, false, map[ResultAction]image.Rectangle{
			ResultQuit: image.Rect(12, 784, 378, 844)}},
		{"再戦できる", true, false, map[ResultAction]image.Rectangle{
			ResultRematch: image.Rect(12, 784, 191, 844),
			ResultQuit:    image.Rect(199, 784, 378, 844)}},
		{"申し出たあと", true, true, map[ResultAction]image.Rectangle{
			ResultQuit: image.Rect(12, 784, 378, 844)}},
	}
	for _, c := range cases {
		got := ResultButtons(l, c.canRematch, c.asked)
		if len(got) != len(c.want) {
			t.Errorf("%s: ボタン数 = %d, 期待 %d", c.name, len(got), len(c.want))
			continue
		}
		for _, b := range got {
			if b.Rect != c.want[b.Action] {
				t.Errorf("%s: %v の矩形 = %v, 期待 %v", c.name, b.Action, b.Rect, c.want[b.Action])
			}
		}
	}
}

// 組み立て中（connecting）は [もどる] 以外が押せない。描画も押下もこの値を見る。
func TestOnlineButtonsWhileConnecting(t *testing.T) {
	l := ComputeLayout(390, 844)
	idle := OnlineButtons(l, false)
	busy := OnlineButtons(l, true)
	for i := range busy {
		if busy[i].Rect != idle[i].Rect {
			t.Errorf("%v: connecting で矩形が動いた %v -> %v", busy[i].Action, idle[i].Rect, busy[i].Rect)
		}
		switch busy[i].Action {
		case OnlineBack:
			if busy[i].Emphasis != EmphasisSecondary {
				t.Errorf("connecting 中の もどる = %v, 期待 Secondary", busy[i].Emphasis)
			}
		default:
			if busy[i].Emphasis != EmphasisDisabled {
				t.Errorf("connecting 中の %v = %v, 期待 Disabled", busy[i].Action, busy[i].Emphasis)
			}
			if idle[i].Emphasis != EmphasisPrimary {
				t.Errorf("connecting でない %v = %v, 期待 Primary", idle[i].Action, idle[i].Emphasis)
			}
		}
	}
}
