package netplay

import (
	"testing"
	"time"

	"github.com/morststs/inkmaru-web/internal/rules"
	"github.com/morststs/inkmaru-web/internal/ui"
)

// この検証台が何を証明し、何を証明しないか。
//
// 証明する: 2人のあいだで状態が食い違うこと。席の取り違え、相手の手の
// 取り違え、片側だけの計算違いは、盤面ハッシュの不一致として現れる。
// 実際、席の割り当ての欠陥はこの台で初めて捕まった。
//
// 証明しない: 両者が「同じ間違い方」をする劣化。両者が同じコードを動かす
// 以上、同じ誤りは同じハッシュを生み、この台は緑のまま通る。実測例として、
// Tick の boardSnap の後始末を消して古い盤面を照合させても、この台は
// 気づかない（気づくのは ui 側の TestConfirmBoardCloneIsHoisted である）。
// つまりこれは「2人の食い違い」の検出器であって、正しさの証明ではない。
// 拡張するときは、片側だけを壊す変異でしか歯が立たないことを憶えておくこと。

// peer は2人対戦のテストで片側のプレイヤーを操る。
//
// 本物の ui.PlayState に本物の NetOpponent を差し、NewPipe の両端でつないである。
// 2つのブラウザを立ち上げずに「本当の2人対戦」に最も近いものを回せる唯一の道具
// なので、Task 6〜9 はこれを拡張して使うとよい。
type peer struct {
	name string
	// opp は相手役。再戦では PlayState だけを作り直して**これを使い回す**
	// （Close すると DataChannel ごと閉じる）。
	opp    *NetOpponent
	state  *ui.PlayState
	moves  []rules.Circle // ターンごとに描く円
	done   map[int]bool   // そのターンを確定済みか
	hashes []string       // ターンが1つ進むたびに記録した盤面ハッシュ
	limits []turnLimit    // ターンごとに、最初に描ける瞬間の残り時間
}

// turnLimit はそのターンで最初に描けるようになった瞬間の Remaining() の答え。
// anchored が偽なら、そのターンには制限時間の起点が無い（＝30秒が効かない）。
type turnLimit struct {
	left     time.Duration
	anchored bool
}

// newTwoPeerMatch は席の違う2人を1本のパイプでつないだ対戦を作る。
//
// 席（seat）は役割で決まる。部屋を作った側が P1、参加した側が P2 である。
// 両者が自分を P1 だと思うと盤面の所有者が鏡像になり、BoardHash が
// セルごとに byte(OwnerAt) を並べる以上、ハッシュは必ず食い違う。
func newTwoPeerMatch(t *testing.T, cfg rules.Config, movesA, movesB []rules.Circle, seatA, seatB rules.Player) (*peer, *peer) {
	t.Helper()
	base := time.Unix(1_700_000_000, 0)
	return newTwoPeerMatchWithClock(t, cfg, movesA, movesB, seatA, seatB, func() time.Time { return base })
}

// newTwoPeerMatchWithClock は時計を外から渡せる版。
//
// **時計が止まったままの台では、時間に関する欠陥が原理的に見つからない。**
// 制限時間の起点を捨て忘れる類の退行は、時計を進めて初めて現れる。
func newTwoPeerMatchWithClock(t *testing.T, cfg rules.Config, movesA, movesB []rules.Circle, seatA, seatB rules.Player, clock func() time.Time) (*peer, *peer) {
	t.Helper()
	ta, tb := NewPipe()

	oa := NewNetOpponent(ta, clock)
	ob := NewNetOpponent(tb, clock)
	oa.Start()
	ob.Start()
	t.Cleanup(func() { oa.Close(); ob.Close() })

	return &peer{name: "ホスト", opp: oa, state: ui.NewPlayStateWithSeat(cfg, oa, seatA), moves: movesA, done: map[int]bool{}},
		&peer{name: "ゲスト", opp: ob, state: ui.NewPlayStateWithSeat(cfg, ob, seatB), moves: movesB, done: map[int]bool{}}
}

// step は1フレームぶん進める。描画フェーズならその場で確定する。
func (p *peer) step() {
	turn := p.state.Match().Turn()
	if p.state.Phase() == ui.PhaseDrawing {
		// そのターンで最初に描けるようになった瞬間の残り時間を控える。
		// ここは相手待ちが終わった直後（Deadline が待ちの期限ではなく
		// そのターンの起点を答える唯一の瞬間）なので、起点が立っているか
		// どうかがそのまま出る。TestTurnLimitIsAnchoredOnEveryTurn が読む。
		if len(p.limits) == turn {
			left, anchored := p.state.Remaining()
			p.limits = append(p.limits, turnLimit{left: left, anchored: anchored})
		}
	}
	if p.state.Phase() == ui.PhaseDrawing && !p.done[turn] && turn < len(p.moves) {
		p.state.SetPreview(p.moves[turn])
		_ = p.state.Confirm()
		p.done[turn] = true
	}
	// リビール演出を待たずに済むよう、1フレームで演出を終わらせる。
	p.state.Tick(1.0)
	for len(p.hashes) < p.state.Match().Turn() {
		p.hashes = append(p.hashes, BoardHash(p.state.Match().Board()))
	}
}

// runTwoPeerMatch は対戦が両者とも終わるまで回す。
func runTwoPeerMatch(t *testing.T, a, b *peer) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if a.state.Phase() == ui.PhaseFinished && b.state.Phase() == ui.PhaseFinished {
			return
		}
		a.step()
		b.step()
	}
	t.Fatalf("対戦が終わらなかった: %s=%v %s=%v", a.name, a.state.Phase(), b.name, b.state.Phase())
}

// 席が違えば、2人は毎ターン同じ盤面を作り、同じ勝敗を見る。
//
// これは第2段階で最も高くついた欠陥を捕まえるテストである。両者が自分を
// P1 として盤面に書き込んでいたため、盤面は互いの鏡像になり、1ターン目の
// ExchangeBoard が必ず board-mismatch になっていた。単一の PlayState しか
// 動かさないテストでは原理的に見つからない。
func TestTwoPeerMatchAgreesOnEveryTurn(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	movesA := []rules.Circle{{CX: 0.3, CY: 0.3, R: 0.2}, {CX: 0.5, CY: 0.5, R: 0.15}, {CX: 0.2, CY: 0.7, R: 0.25}}
	movesB := []rules.Circle{{CX: 0.7, CY: 0.7, R: 0.18}, {CX: 0.4, CY: 0.6, R: 0.3}, {CX: 0.8, CY: 0.2, R: 0.12}}
	a, b := newTwoPeerMatch(t, cfg, movesA, movesB, rules.P1, rules.P2)

	runTwoPeerMatch(t, a, b)

	for _, p := range []*peer{a, b} {
		if p.state.AbortReason() != "" {
			t.Fatalf("%s が中断した: %s", p.name, p.state.AbortReason())
		}
		if p.state.PeerLeft() {
			t.Fatalf("%s の側で相手が消えたことになっている", p.name)
		}
	}
	if len(a.hashes) != cfg.Turns || len(b.hashes) != cfg.Turns {
		t.Fatalf("記録したターン数が違う: %d と %d, 期待 %d", len(a.hashes), len(b.hashes), cfg.Turns)
	}
	for i := range a.hashes {
		if a.hashes[i] != b.hashes[i] {
			t.Errorf("ターン %d の盤面ハッシュが食い違う: %s=%s %s=%s",
				i, a.name, a.hashes[i][:16], b.name, b.hashes[i][:16])
		}
	}
	if ra, rb := a.state.Match().Result(), b.state.Match().Result(); ra != rb {
		t.Errorf("勝敗が食い違う: %s=%v %s=%v", a.name, ra, b.name, rb)
	}
	if a.state.Seat() != rules.P1 || b.state.Seat() != rules.P2 {
		t.Errorf("席 = %v / %v, 期待 P1 / P2", a.state.Seat(), b.state.Seat())
	}
}

// 席が同じだと盤面は鏡像になり、1ターン目で board-mismatch になる。
// 上のテストに歯があることの裏返しであり、修正前の実装そのものでもある。
func TestSameSeatBreaksTheBoardExchange(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	movesA := []rules.Circle{{CX: 0.3, CY: 0.3, R: 0.2}, {CX: 0.5, CY: 0.5, R: 0.15}, {CX: 0.2, CY: 0.7, R: 0.25}}
	movesB := []rules.Circle{{CX: 0.7, CY: 0.7, R: 0.18}, {CX: 0.4, CY: 0.6, R: 0.3}, {CX: 0.8, CY: 0.2, R: 0.12}}
	a, b := newTwoPeerMatch(t, cfg, movesA, movesB, rules.P1, rules.P1)

	runTwoPeerMatch(t, a, b)

	if a.hashes[0] == b.hashes[0] {
		t.Fatal("両者が同じ席なのに盤面ハッシュが一致した。ハッシュが所有者を見ていない")
	}
	if a.state.AbortReason() == "" && b.state.AbortReason() == "" {
		t.Error("盤面が食い違ったのに、どちらも中断していない")
	}
}

// 毎ターン、制限時間の起点が立っている。
//
// **これは層の境目を守るテストである。** 30秒の起点は ui.PlayState が
// NetOpponent.ConfirmBoard へ渡すターン番号で決まる（ConfirmBoard は一致した
// 時点で markStart(turn+1) を呼ぶ）。渡す番号が1つずれると、ターン1以降の
// 起点がどこにも立たず、Deadline は ok=false を返し、仕様書 §7.1 の30秒が
// 音もなく無効になる。ずれても両者が同じずれ方をするので盤面ハッシュは
// 一致し続け、上の TestTwoPeerMatchAgreesOnEveryTurn は緑のまま通る
// （このファイル冒頭が「証明しない」と書いた性質そのものである）。
// netplay 側の単体テストは NetOpponent を正しい番号で直接叩き、ui 側の
// 制限時間テストは偽の相手を使うので、この結び付きを見ているのはここだけである。
//
// 見るのは盤面ハッシュではなく残り時間である。時計は止めてあるので、
// 起点が立っていれば残りはちょうど TurnLimit になる。
func TestTurnLimitIsAnchoredOnEveryTurn(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	movesA := []rules.Circle{{CX: 0.3, CY: 0.3, R: 0.2}, {CX: 0.5, CY: 0.5, R: 0.15}, {CX: 0.2, CY: 0.7, R: 0.25}}
	movesB := []rules.Circle{{CX: 0.7, CY: 0.7, R: 0.18}, {CX: 0.4, CY: 0.6, R: 0.3}, {CX: 0.8, CY: 0.2, R: 0.12}}
	a, b := newTwoPeerMatch(t, cfg, movesA, movesB, rules.P1, rules.P2)

	runTwoPeerMatch(t, a, b)

	for _, p := range []*peer{a, b} {
		if len(p.limits) != cfg.Turns {
			t.Fatalf("%s: 残り時間を控えたターン数 = %d, 期待 %d", p.name, len(p.limits), cfg.Turns)
		}
		for turn, lim := range p.limits {
			if !lim.anchored {
				t.Errorf("%s: ターン %d に制限時間の起点が無い（§7.1 の30秒が効かない）", p.name, turn)
				continue
			}
			if lim.left != TurnLimit {
				t.Errorf("%s: ターン %d の残り時間 = %v, 期待 %v（時計は止めてある）",
					p.name, turn, lim.left, TurnLimit)
			}
		}
	}
}

// 再戦して、2戦とも両者が同じ盤面と同じ勝敗を見る。
//
// **これが再戦の本番に最も近い検証である。** 対戦1の状態が少しでも残っていれば、
// 対戦2は盤面ハッシュの食い違い（board-mismatch）か protocol 中断で壊れる。
// 単体のセッションだけを見るテストでは、両者の食い違いは原理的に見つからない。
func TestRematchPlaysASecondMatch(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	movesA := []rules.Circle{{CX: 0.3, CY: 0.3, R: 0.2}, {CX: 0.5, CY: 0.5, R: 0.15}, {CX: 0.2, CY: 0.7, R: 0.25}}
	movesB := []rules.Circle{{CX: 0.7, CY: 0.7, R: 0.18}, {CX: 0.4, CY: 0.6, R: 0.3}, {CX: 0.8, CY: 0.2, R: 0.12}}
	clock := time.Unix(1_700_000_000, 0)
	a, b := newTwoPeerMatchWithClock(t, cfg, movesA, movesB, rules.P1, rules.P2, func() time.Time { return clock })

	runTwoPeerMatch(t, a, b)
	// 対戦1を戦い、結果を読み、相手の返事を待つ時間が実際には流れる。
	clock = clock.Add(5 * time.Minute)
	for _, p := range []*peer{a, b} {
		if !p.state.CanRematch() {
			t.Fatalf("%s に再戦が出ていない (中断=%q 離脱=%v)",
				p.name, p.state.AbortReason(), p.state.PeerLeft())
		}
		if err := p.state.RequestRematch(); err != nil {
			t.Fatalf("%s の RequestRematch: %v", p.name, err)
		}
	}

	// 両者が押したので、次のフレームで成立する。
	for i := 0; i < 50; i++ {
		oka, err := a.state.PollRematch()
		if err != nil {
			t.Fatalf("%s の PollRematch: %v", a.name, err)
		}
		okb, err := b.state.PollRematch()
		if err != nil {
			t.Fatalf("%s の PollRematch: %v", b.name, err)
		}
		if oka && okb {
			break
		}
		if i == 49 {
			t.Fatalf("再戦が成立しなかった (%s=%v %s=%v)", a.name, oka, b.name, okb)
		}
	}

	// 同じ相手役のまま、新しい対戦を始める。Close は呼ばない（接続が死ぬ）。
	movesA2 := []rules.Circle{{CX: 0.6, CY: 0.2, R: 0.22}, {CX: 0.3, CY: 0.5, R: 0.17}, {CX: 0.7, CY: 0.8, R: 0.2}}
	movesB2 := []rules.Circle{{CX: 0.2, CY: 0.8, R: 0.16}, {CX: 0.6, CY: 0.4, R: 0.28}, {CX: 0.3, CY: 0.3, R: 0.14}}
	a2 := &peer{name: a.name, state: ui.NewPlayStateWithSeat(cfg, a.opp, a.state.Seat()), moves: movesA2, done: map[int]bool{}}
	b2 := &peer{name: b.name, state: ui.NewPlayStateWithSeat(cfg, b.opp, b.state.Seat()), moves: movesB2, done: map[int]bool{}}

	// **対戦2の制限時間は、対戦2が始まった時点から数え直される。**
	// 対戦1の起点を捨て忘れると、5分前の起点＋30秒が期限になり、対戦2は
	// 始まった瞬間に時間切れになる（設計 §4）。
	for _, p := range []*peer{a2, b2} {
		d, ok := p.state.Remaining()
		if !ok {
			t.Fatalf("再戦で %s の制限時間が立っていない", p.name)
		}
		if d <= 0 {
			t.Fatalf("再戦の開始時点で %s の残り時間が %v。対戦1の起点を引きずっている", p.name, d)
		}
	}

	runTwoPeerMatch(t, a2, b2)

	for _, p := range []*peer{a2, b2} {
		if p.state.AbortReason() != "" {
			t.Fatalf("再戦で %s が中断した: %s", p.name, p.state.AbortReason())
		}
		if p.state.PeerLeft() {
			t.Fatalf("再戦で %s の側から相手が消えた", p.name)
		}
	}
	if len(a2.hashes) != cfg.Turns || len(b2.hashes) != cfg.Turns {
		t.Fatalf("再戦で記録したターン数が違う: %d と %d", len(a2.hashes), len(b2.hashes))
	}
	for i := range a2.hashes {
		if a2.hashes[i] != b2.hashes[i] {
			t.Errorf("再戦のターン %d の盤面ハッシュが食い違う: %s=%s %s=%s",
				i, a2.name, a2.hashes[i][:16], b2.name, b2.hashes[i][:16])
		}
	}
	if ra, rb := a2.state.Match().Result(), b2.state.Match().Result(); ra != rb {
		t.Errorf("再戦の勝敗が食い違う: %s=%v %s=%v", a2.name, ra, b2.name, rb)
	}
	// 対戦1と対戦2は違う手を打っているので、同じ盤面になるはずがない。
	// 同じなら、対戦1の盤面を引きずっている。
	if a.hashes[cfg.Turns-1] == a2.hashes[cfg.Turns-1] {
		t.Error("再戦の最終盤面が対戦1と同じ。盤面を引きずっている")
	}
}

// 片方だけが押しても、再戦は始まらない。
func TestRematchNeedsBothSides(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	movesA := []rules.Circle{{CX: 0.3, CY: 0.3, R: 0.2}, {CX: 0.5, CY: 0.5, R: 0.15}, {CX: 0.2, CY: 0.7, R: 0.25}}
	movesB := []rules.Circle{{CX: 0.7, CY: 0.7, R: 0.18}, {CX: 0.4, CY: 0.6, R: 0.3}, {CX: 0.8, CY: 0.2, R: 0.12}}
	a, b := newTwoPeerMatch(t, cfg, movesA, movesB, rules.P1, rules.P2)

	runTwoPeerMatch(t, a, b)
	if err := a.state.RequestRematch(); err != nil {
		t.Fatalf("a の RequestRematch: %v", err)
	}
	for i := 0; i < 20; i++ {
		ok, err := a.state.PollRematch()
		if err != nil {
			t.Fatalf("a の PollRematch: %v", err)
		}
		if ok {
			t.Fatal("片方だけで再戦が成立した")
		}
		if _, err := b.state.PollRematch(); err != nil {
			t.Fatalf("b の PollRematch: %v", err)
		}
	}
	if d, ok := a.state.RematchRemaining(); !ok || d <= 0 {
		t.Errorf("待ち時間が出ていない: %v (ok=%v)", d, ok)
	}
}
