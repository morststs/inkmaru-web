package ui

import (
	"testing"

	"github.com/morststs/inkmaru-web/internal/rules"
)

func TestRenderBoardPixelsLength(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 16
	px := RenderBoardPixels(rules.NewBoard(cfg), nil)
	if want := 4 * 16 * 16; len(px) != want {
		t.Fatalf("バイト数 = %d, 期待 %d", len(px), want)
	}
}

// 未取得のセルは透明で書き出す。紙（gfx が下に敷く層）が透けるようにするため。
//
// **ここを不透明に戻すと、紙の地合いが完全に隠れる。** 盤面は一枚の画像として
// 正しく見えてしまうので、気づけるのはこのテストだけである。
func TestUnclaimedCellsAreTransparent(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 16
	px := RenderBoardPixels(rules.NewBoard(cfg), nil)
	for i := 0; i < len(px); i += 4 {
		if px[i] != 0 || px[i+1] != 0 || px[i+2] != 0 || px[i+3] != 0 {
			t.Fatalf("未取得のセルが透明でない: offset=%d %v", i, px[i:i+4])
		}
	}
}

func TestRenderBoardPixelsBlendsOwnerColour(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 32
	b := rules.NewBoard(cfg)
	b.Apply(rules.Circle{CX: 0.5, CY: 0.5, R: cfg.MaxRadius()}, rules.P1, cfg.InkPerTurn)

	px := RenderBoardPixels(b, nil)
	cx, cy := cfg.GridN/2, cfg.GridN/2
	if got := b.OwnerAt(cx, cy); got != rules.P1 {
		t.Fatalf("前提が崩れている: 中心の所有者 = %v", got)
	}
	a := AlphaForDensity(cfg, b.DensityAt(cx, cy))
	i := 4 * (cy*cfg.GridN + cx)
	wantA := uint8(a*255 + 0.5)
	if diff := int(px[i+3]) - int(wantA); diff > 1 || diff < -1 {
		t.Errorf("A成分 = %d, 期待 %d (alpha=%v)", px[i+3], wantA, a)
	}
	// 色は紙と混ぜずに、陣地の色そのものを premultiplied で持つ。
	wantR := uint8(float64(ColorP1.R)*float64(wantA)/255 + 0.5)
	if diff := int(px[i]) - int(wantR); diff > 1 || diff < -1 {
		t.Errorf("R成分 = %d, 期待 %d", px[i], wantR)
	}
}

// 書き出したバイト列は premultiplied alpha でなければならない。
//
// **ebiten.Image.WritePixels がそれを要求する。** かつて A=255 で書いていた
// 頃は premultiplied 値と素の値が一致していたので取り違えようがなかったが、
// 半透明にすると一致しなくなる。**間違えても落ちず、色が濃すぎる／薄すぎる
// だけの壊れ方をする。**
func TestBoardPixelsArePremultiplied(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 32
	b := rules.NewBoard(cfg)
	b.Apply(rules.Circle{CX: 0.5, CY: 0.5, R: cfg.MaxRadius()}, rules.P1, cfg.InkPerTurn)

	px := RenderBoardPixels(b, nil)
	cx, cy := cfg.GridN/2, cfg.GridN/2
	if got := b.OwnerAt(cx, cy); got != rules.P1 {
		t.Fatalf("前提が崩れている: 中心の所有者 = %v", got)
	}
	i := 4 * (cy*cfg.GridN + cx)
	a := px[i+3]
	if a == 0 || a == 0xFF {
		t.Fatalf("中心のアルファが %d で、premultiplied かどうかを判定できない", a)
	}
	// premultiplied を素に戻すと、陣地の色に一致するはずである。
	f := 255 / float64(a)
	for ch, want := range map[int]uint8{0: ColorP1.R, 1: ColorP1.G, 2: ColorP1.B} {
		got := float64(px[i+ch]) * f
		if diff := got - float64(want); diff > 2 || diff < -2 {
			t.Errorf("成分 %d を素に戻すと %.1f、期待 %d（premultiplied で書けていない）", ch, got, want)
		}
	}
	// どの画素も premultiplied の不変条件（各成分 <= A）を満たすこと。
	for j := 0; j < len(px); j += 4 {
		for ch := 0; ch < 3; ch++ {
			if px[j+ch] > px[j+3] {
				t.Fatalf("offset=%d: 成分 %d (%d) がアルファ (%d) を超えている", j, ch, px[j+ch], px[j+3])
			}
		}
	}
}

func TestRenderBoardPixelsHatchesNeutral(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	b := rules.NewBoard(cfg)
	// 同じ大きさの円をぶつけると、その領域は丸ごと中立になる。
	b.Apply(rules.Circle{CX: 0.5, CY: 0.5, R: 0.3}, rules.P1, cfg.InkPerTurn)
	b.Apply(rules.Circle{CX: 0.5, CY: 0.5, R: 0.3}, rules.P2, cfg.InkPerTurn)
	if got := b.OwnerAt(32, 32); got != rules.Neutral {
		t.Fatalf("前提が崩れている: 中心の所有者 = %v", got)
	}

	px := RenderBoardPixels(b, nil)
	// 中立領域を横に走査すると、縞のためピクセルが2種類以上現れる。
	seen := map[[4]byte]bool{}
	for x := 24; x < 40; x++ {
		i := 4 * (32*cfg.GridN + x)
		seen[[4]byte{px[i], px[i+1], px[i+2], px[i+3]}] = true
	}
	if len(seen) < 2 {
		t.Errorf("中立領域が単色で描かれている（ハッチングになっていない）: %d 色", len(seen))
	}

	// 隣接する2つの縞バンドのアルファが十分に違うこと。
	i1 := 4 * (32*cfg.GridN + 24)
	i2 := 4 * (32*cfg.GridN + 28)
	diff := int(px[i1+3]) - int(px[i2+3])
	if diff < 0 {
		diff = -diff
	}
	if diff < 60 {
		t.Errorf("縞の対比が不十分（アルファ差 = %d, 期待 >= 60）", diff)
	}
}

func TestRenderBoardPixelsReusesBuffer(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 16
	buf := make([]byte, 4*16*16)
	got := RenderBoardPixels(rules.NewBoard(cfg), buf)
	if &got[0] != &buf[0] {
		t.Error("十分な容量のバッファが再利用されていない")
	}
}

// インクの縁を足しても、最大濃度の内側より濃く見えてはならない。
//
// **守るのは「最大濃度の内側（AlphaHi）を縁が超えないこと」だけである。**
// 薄い陣地の縁が、最も濃い陣地の内側より濃く見えるのを防ぐ。
//
// **守らないもの:** 中間の濃度との逆転。edgeGain は 0.10 で、表示アルファの
// 全幅 0.66 の約 15% なので、濃度の差がそれ未満の2つの陣地は、境界で
// 縁を足した側と足さない側の順序が入れ替わって見えうる。頭打ちは
// 最大値にしか効かない。それを許すかどうかは edgeGain の調整の話で、
// このテストの範囲ではない（仕様書 §3.7「濃い＝強い」）。
func TestInkEdgeNeverExceedsMaxDensityAlpha(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	b := rules.NewBoard(cfg)
	b.Apply(rules.Circle{CX: 0.35, CY: 0.5, R: cfg.MaxRadius()}, rules.P1, cfg.InkPerTurn)
	b.Apply(rules.Circle{CX: 0.65, CY: 0.5, R: cfg.MinRadius()}, rules.P2, cfg.InkPerTurn)

	px := RenderBoardPixels(b, nil)
	hi := AlphaHi // 定数のままでは uint8 への変換がコンパイルできない
	limit := uint8(hi*255 + 0.5)
	for i := 3; i < len(px); i += 4 {
		if px[i] > limit {
			t.Fatalf("offset=%d のアルファ %d が上限 %d を超えた", i-3, px[i], limit)
		}
	}
}

// 縁を足すのは持ち主の変わる境界だけである。
//
// **盤面が1人のもので埋まっていても、内側が縁になってはならない。**
// 全セルを縁と判定する実装は、見た目は「少し濃い盤面」にしかならず、
// 目視では気づけない。
func TestInkEdgeOnlyOnOwnerBoundary(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	b := rules.NewBoard(cfg)
	// 盤面を覆う大きな円。濃度が一様な中心付近を見る。
	b.Apply(rules.Circle{CX: 0.5, CY: 0.5, R: cfg.MaxRadius()}, rules.P1, cfg.InkPerTurn)
	px := RenderBoardPixels(b, nil)

	cx, cy := cfg.GridN/2, cfg.GridN/2
	// 前提: 円は盤面を覆わず（隅は未取得）、中心の素のアルファは縁を足しても
	// 頭打ちに掛からない。MaxArea が変わってここが崩れると、下の照合が無力になる。
	if b.OwnerAt(0, 0) != rules.None {
		t.Fatal("前提が崩れた: 円が盤面を覆っている")
	}
	if base := AlphaForDensity(cfg, b.DensityAt(cx, cy)); base+edgeGain > AlphaHi {
		t.Fatalf("前提が崩れた: 素のアルファ %.3f が頭打ちに掛かる", base)
	}
	inner := px[4*(cy*cfg.GridN+cx)+3]
	want := uint8(AlphaForDensity(cfg, b.DensityAt(cx, cy))*255 + 0.5)
	if diff := int(inner) - int(want); diff > 1 || diff < -1 {
		t.Errorf("内側のアルファ = %d, 期待 %d（縁が内側にも足されている）", inner, want)
	}
}

// 空の盤面には縁が1つも無い。
func TestInkEdgeOnEmptyBoard(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 16
	px := RenderBoardPixels(rules.NewBoard(cfg), nil)
	for i := 3; i < len(px); i += 4 {
		if px[i] != 0 {
			t.Fatalf("空の盤面に不透明な画素がある: offset=%d alpha=%d", i-3, px[i])
		}
	}
}

// GridN が小さくても盤面の外を読まない。
//
// **盤面の端のセルは、隣が存在しない向きを持つ。** 範囲検査を落とすと
// index out of range で落ちるか、別の行の画素を隣と誤認する。
func TestInkEdgeDoesNotReadOutOfBounds(t *testing.T) {
	for _, n := range []int{1, 2, 3, 16, 17} {
		cfg := rules.DefaultConfig()
		cfg.GridN = n
		b := rules.NewBoard(cfg)
		b.Apply(rules.Circle{CX: 0.5, CY: 0.5, R: cfg.MaxRadius()}, rules.P1, cfg.InkPerTurn)
		px := RenderBoardPixels(b, nil) // パニックしなければよい
		if len(px) != 4*n*n {
			t.Fatalf("GridN=%d: バイト数 = %d, 期待 %d", n, len(px), 4*n*n)
		}
	}
}

// 盤面全体が1人のものでも、縁になるのは盤面の外周だけである。
//
// GridN が 256 以外でも成り立つこと、走査が効いていること（内側を1つ以上
// 見たこと）を下限で確かめる。
func TestInkEdgeOnlyOnPerimeterWhenBoardIsOwnedByOne(t *testing.T) {
	for _, n := range []int{16, 32, 64} {
		cfg := rules.DefaultConfig()
		cfg.GridN = n
		b := rules.NewBoard(cfg)
		// 盤面を完全に覆う円。濃度は一様ではないので、素のアルファとの差で見る。
		b.Apply(rules.Circle{CX: 0.5, CY: 0.5, R: 2}, rules.P1, cfg.InkPerTurn)
		px := RenderBoardPixels(b, nil)

		inner, perimeter := 0, 0
		for y := 0; y < n; y++ {
			for x := 0; x < n; x++ {
				if b.OwnerAt(x, y) != rules.P1 {
					t.Fatalf("GridN=%d: (%d,%d) が P1 でない。前提が崩れた", n, x, y)
				}
				base := AlphaForDensity(cfg, b.DensityAt(x, y))
				want := base
				onEdge := x == 0 || y == 0 || x == n-1 || y == n-1
				if onEdge {
					want = base + edgeGain
					if want > AlphaHi {
						want = AlphaHi
					}
					perimeter++
				} else {
					inner++
				}
				got := px[4*(y*n+x)+3]
				w := uint8(want*255 + 0.5)
				if diff := int(got) - int(w); diff > 1 || diff < -1 {
					t.Fatalf("GridN=%d (%d,%d): アルファ %d, 期待 %d（外周=%v）", n, x, y, got, w, onEdge)
				}
			}
		}
		if inner < (n-2)*(n-2) || perimeter != 4*n-4 {
			t.Fatalf("GridN=%d: 走査が足りない inner=%d perimeter=%d", n, inner, perimeter)
		}
	}
}

// checkInkEdges は盤面の全セルについて、縁の有無を独立に数え直して照合する。
//
// 四近傍のどれかが盤面の外か別の持ち主なら縁（素のアルファ+edgeGain を
// AlphaHi で頭打ち）、そうでなければ素のままである。**走査が効いたことを
// 確かめるため、縁・内側・「縁が頭打ちされず実際に濃くなったセル」の件数を返す。**
func checkInkEdges(t *testing.T, b *rules.Board) (edge, inner, visible int) {
	t.Helper()
	cfg := b.Config()
	n := b.GridN()
	px := RenderBoardPixels(b, nil)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			owner := b.OwnerAt(x, y)
			if owner != rules.P1 && owner != rules.P2 {
				continue
			}
			base := AlphaForDensity(cfg, b.DensityAt(x, y))
			onEdge := false
			for _, nb := range [][2]int{{x - 1, y}, {x + 1, y}, {x, y - 1}, {x, y + 1}} {
				if nb[0] < 0 || nb[1] < 0 || nb[0] >= n || nb[1] >= n || b.OwnerAt(nb[0], nb[1]) != owner {
					onEdge = true
				}
			}
			want := base
			if onEdge {
				want = base + edgeGain
				if want > AlphaHi {
					want = AlphaHi
				}
				edge++
				if want > base+1e-9 {
					visible++
				}
			} else {
				inner++
			}
			got := px[4*(y*n+x)+3]
			w := uint8(want*255 + 0.5)
			if diff := int(got) - int(w); diff > 1 || diff < -1 {
				t.Fatalf("(%d,%d) owner=%v: アルファ %d, 期待 %d（縁=%v）", x, y, owner, got, w, onEdge)
			}
		}
	}
	return edge, inner, visible
}

// 盤面の内側で持ち主の変わる境界にも縁が付く。
//
// **外周しか縁にしない実装は、外周のテストだけでは捕まらない。** 小円1つ
// （陣地と未取得の境界）と、隣り合う2円（陣地同士の境界）の両方で、
// 境界の両側が濃くなり、1セル離れたセルは素のままであることを見る。
func TestInkEdgeOnInteriorOwnerBoundary(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 32

	one := rules.NewBoard(cfg)
	one.Apply(rules.Circle{CX: 0.5, CY: 0.5, R: 0.25}, rules.P1, cfg.InkPerTurn)
	// 前提: 円は盤面の内側に収まり、中心は内側のセルである。
	if one.OwnerAt(0, 0) != rules.None || one.OwnerAt(16, 16) != rules.P1 {
		t.Fatal("前提が崩れた: 円が盤面を覆うか、中心が陣地でない")
	}
	if base := AlphaForDensity(cfg, one.DensityAt(16, 16)); base+edgeGain > AlphaHi {
		t.Fatalf("前提が崩れた: 素のアルファ %.3f が頭打ちに掛かり、縁が見えない", base)
	}
	e, in, vis := checkInkEdges(t, one)
	if e < 20 || in < 100 || vis != e {
		t.Errorf("小円: 走査が足りない edge=%d inner=%d visible=%d", e, in, vis)
	}

	two := rules.NewBoard(cfg)
	two.Apply(rules.Circle{CX: 0.32, CY: 0.5, R: 0.2}, rules.P1, cfg.InkPerTurn)
	two.Apply(rules.Circle{CX: 0.68, CY: 0.5, R: 0.2}, rules.P2, cfg.InkPerTurn)
	// 前提: 2円は接し、P1・P2 が横に隣り合うセルがある。
	adjacent := false
	for y := 0; y < 32; y++ {
		for x := 0; x < 31; x++ {
			if two.OwnerAt(x, y) == rules.P1 && two.OwnerAt(x+1, y) == rules.P2 {
				adjacent = true
			}
		}
	}
	if !adjacent {
		t.Fatal("前提が崩れた: P1 と P2 が隣り合っていない")
	}
	e, in, _ = checkInkEdges(t, two)
	if e < 40 || in < 100 {
		t.Errorf("2円: 走査が足りない edge=%d inner=%d", e, in)
	}
}
