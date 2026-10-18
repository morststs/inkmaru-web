package ui

import (
	"strings"
	"testing"

	"github.com/morststs/inkmaru-web/internal/rules"
)

func TestPeerOf(t *testing.T) {
	if got := PeerOf(rules.P1); got != rules.P2 {
		t.Errorf("PeerOf(P1) = %v, 期待 P2", got)
	}
	if got := PeerOf(rules.P2); got != rules.P1 {
		t.Errorf("PeerOf(P2) = %v, 期待 P1", got)
	}
}

// TestScoreLabelsKeepBoardOrder は帯グラフの並びが席で変わらないことを守る。
// 席で並べ替えると、同じ盤面が2つの画面で違って見える。
func TestScoreLabelsKeepBoardOrder(t *testing.T) {
	want := [3]rules.Player{rules.P1, rules.Neutral, rules.P2}
	for _, seat := range []rules.Player{rules.P1, rules.P2} {
		got := ScoreLabels(seat, "相手")
		for i := range want {
			if got[i].Player != want[i] {
				t.Errorf("席%v: 区画%d = %v, 期待 %v", seat, i, got[i].Player, want[i])
			}
		}
	}
}

// TestScoreLabelsFollowSeat は「あなた」が自分の席に付くことを守る。
//
// ここが席に依らず P1 固定だと、ゲスト（P2）の画面では相手の割合が
// 「あなた」として出る。第2段階で最も高くついた欠陥と同じ形である。
func TestScoreLabelsFollowSeat(t *testing.T) {
	cases := []struct {
		seat        rules.Player
		peerName    string
		p1Label     string
		p2Label     string
		neutralWant string
	}{
		{rules.P1, "CPU", NameYou, "CPU", NameNeutral},
		{rules.P2, "相手", "相手", NameYou, NameNeutral},
	}
	for _, c := range cases {
		got := ScoreLabels(c.seat, c.peerName)
		if got[0].Label != c.p1Label {
			t.Errorf("席%v: P1 の見出し = %q, 期待 %q", c.seat, got[0].Label, c.p1Label)
		}
		if got[1].Label != c.neutralWant {
			t.Errorf("席%v: 中立の見出し = %q, 期待 %q", c.seat, got[1].Label, c.neutralWant)
		}
		if got[2].Label != c.p2Label {
			t.Errorf("席%v: P2 の見出し = %q, 期待 %q", c.seat, got[2].Label, c.p2Label)
		}
	}
}

// TestResultMessageFollowsSeat は勝敗の文言が席から決まることを守る。
//
// **P2 が勝ったときにゲスト（席=P2）が「あなたの勝ち」を見ること**が要点である。
// 決め打ちのままだと、ゲストは勝った対戦で「相手の勝ち」と表示される。
//
// rules.InProgress（3ターン戦い切らずに中断した対戦）を「引き分け」と
// 呼ばないことも、ここで固定する。「引き分け」は「戦って同点だった」と読める。
func TestResultMessageFollowsSeat(t *testing.T) {
	cases := []struct {
		res  rules.Result
		seat rules.Player
		peer string
		want string
	}{
		{rules.WinP1, rules.P1, "CPU", "あなたの勝ち"},
		{rules.WinP1, rules.P2, "相手", "相手の勝ち"},
		{rules.WinP2, rules.P1, "CPU", "CPUの勝ち"},
		{rules.WinP2, rules.P2, "相手", "あなたの勝ち"},
		{rules.Draw, rules.P1, "CPU", "引き分け"},
		{rules.Draw, rules.P2, "相手", "引き分け"},
		{rules.InProgress, rules.P1, "相手", "勝敗なし（中断）"},
		{rules.InProgress, rules.P2, "相手", "勝敗なし（中断）"},
	}
	for _, c := range cases {
		if got := ResultMessage(c.res, c.seat, c.peer, false); got != c.want {
			t.Errorf("ResultMessage(%v, %v, %q, false) = %q, 期待 %q", c.res, c.seat, c.peer, got, c.want)
		}
	}
}

// TestResultMessagePeerLeftOverridesBoard は仕様書 §5.4 のゲーム規則を守る。
//
// **相手が離れたら自分の勝ちである。盤面の集計に上書きさせてはならない。**
// commit が揃ったあと、先に実体を送った側の情報を見てから「不利だから切断する」
// ことは技術的に防げないので、逃げても勝てないようにして動機を消している。
// 上書きが消えると、負けている側は切断すれば負けを避けられる。
func TestResultMessagePeerLeftOverridesBoard(t *testing.T) {
	// 盤面の上では相手が勝っている（席 P2 の自分に対して WinP1）が、
	// 相手が離れたのだから自分の勝ちでなければならない。
	if got := ResultMessage(rules.WinP1, rules.P2, "相手", true); got != "あなたの勝ち" {
		t.Errorf("席P2・盤面はWinP1・相手が離脱 → %q, 期待 %q", got, "あなたの勝ち")
	}
	if got := ResultMessage(rules.WinP2, rules.P1, "相手", true); got != "あなたの勝ち" {
		t.Errorf("席P1・盤面はWinP2・相手が離脱 → %q, 期待 %q", got, "あなたの勝ち")
	}
	// 中断で終わった対戦でも、相手が離れたのなら勝敗はつく。
	if got := ResultMessage(rules.InProgress, rules.P2, "相手", true); got != "あなたの勝ち" {
		t.Errorf("中断＋相手が離脱 → %q, 期待 %q", got, "あなたの勝ち")
	}
	// 逆に、自分側の失敗（PeerLeft が偽）では勝敗をつけない。
	if got := ResultMessage(rules.InProgress, rules.P1, "相手", false); got == "あなたの勝ち" {
		t.Errorf("相手が離れていないのに %q", got)
	}
}

// TestResultMessageMatchesPlayStateSeat は、実際の対戦から取り出した席と
// 結果が噛み合うことを確かめる。ResultMessage 単体の表と違い、
// PlayState.Seat() が返す値の意味（どちらの得点が自分か）まで含めて守る。
func TestResultMessageMatchesPlayStateSeat(t *testing.T) {
	cfg := rules.DefaultConfig()
	// 席 P2 で、P2 が圧勝する対戦を仕立てる。相手（P1）は最小の円しか描かない。
	s := NewPlayStateWithSeat(cfg, &fixedOpponent{c: rules.Circle{CX: 0.05, CY: 0.05, R: cfg.MinRadius()}}, rules.P2)
	for i := 0; i < cfg.Turns; i++ {
		s.SetPreview(rules.Circle{CX: 0.5, CY: 0.5, R: cfg.MaxRadius()})
		if err := s.Confirm(); err != nil {
			t.Fatalf("ターン%d の Confirm: %v", i, err)
		}
		// リビール演出を終わらせて次のターンへ進める。
		for j := 0; j < 200 && s.Phase() == PhaseRevealing; j++ {
			s.Tick(0.1)
		}
	}
	if s.Phase() != PhaseFinished {
		t.Fatalf("対戦が終わっていない: phase = %v", s.Phase())
	}
	if s.Match().Result() != rules.WinP2 {
		t.Fatalf("この仕立てでは P2 が勝つはず: %v", s.Match().Result())
	}
	if got := ResultMessage(s.Match().Result(), s.Seat(), "相手", false); got != "あなたの勝ち" {
		t.Errorf("席 P2 で P2 が勝ったのに %q", got)
	}
}

// fixedOpponent は常に同じ円を返す相手。
type fixedOpponent struct{ c rules.Circle }

func (o *fixedOpponent) Submit(rules.Circle, int) error       { return nil }
func (o *fixedOpponent) Poll(int) (rules.Circle, bool, error) { return o.c, true, nil }
func (o *fixedOpponent) Close() error                         { return nil }

// TestResultDetailKeepsAbortReasonIdentifier は、中断の理由が
// 末尾まで読めることを守る。
//
// **理由の値打ちは末尾の識別子にある。** `board-mismatch` や `hash-mismatch` が
// 「…」で落ちると、何が起きたのか分からないまま「理由」とだけ書いてある行が残る。
// 390px 幅（InfoCols で30桁）・3行のときに、相手の離脱の1行と同時に出しても
// 識別子が生き残ることを固定する。
func TestResultDetailKeepsAbortReasonIdentifier(t *testing.T) {
	var s rules.Score
	const cols, maxLines = 30, 3
	for _, reason := range []string{
		"相手のデータが不正でした（hash-mismatch）",
		"相手のデータが不正でした（board-mismatch）",
	} {
		for _, peerLeft := range []bool{false, true} {
			lines := ResultDetail(s, "相手", reason, peerLeft, cols, maxLines)
			joined := strings.Join(lines, "")
			for _, want := range []string{"hash-mismatch", "board-mismatch"} {
				if strings.Contains(reason, want) && !strings.Contains(joined, want) {
					t.Errorf("peerLeft=%v: %q が落ちた: %q", peerLeft, want, lines)
				}
			}
			if len(lines) > maxLines {
				t.Errorf("peerLeft=%v: %d 行 (上限 %d): %q", peerLeft, len(lines), maxLines, lines)
			}
			for _, line := range lines {
				if n := len([]rune(line)); n > cols {
					t.Errorf("peerLeft=%v: 行が %d 文字 (%q)", peerLeft, n, line)
				}
			}
		}
	}
}

// TestResultDetailShowsPeerLeftAndReasonIndependently は、
// PeerLeft と AbortReason が直交することを守る（playstate.go の注釈）。
// 素の切断では前者だけ、相手が原因の中断では両方、自分側の失敗では後者だけ。
func TestResultDetailShowsPeerLeftAndReasonIndependently(t *testing.T) {
	var s rules.Score
	const cols, maxLines = 30, 3

	// 素の切断: 離脱の行が出て、理由の行は出ない。割合の行は出る。
	lines := ResultDetail(s, "相手", "", true, cols, maxLines)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "相手が対戦から離れました") {
		t.Errorf("素の切断で離脱の行が無い: %q", lines)
	}
	if strings.Contains(joined, "理由") {
		t.Errorf("理由が無いのに理由の行が出た: %q", lines)
	}
	if !strings.Contains(joined, "中立") {
		t.Errorf("理由が無いなら割合を出すはず: %q", lines)
	}

	// 自分側の失敗: 理由だけが出て、離脱の行は出ない。
	lines = ResultDetail(s, "相手", "ui: ターンを解決できなかった", false, cols, maxLines)
	joined = strings.Join(lines, "\n")
	if strings.Contains(joined, "離れました") {
		t.Errorf("相手は離れていないのに離脱の行が出た: %q", lines)
	}
	if !strings.Contains(joined, "ターンを解決できなかった") {
		t.Errorf("理由が出ていない: %q", lines)
	}

	// 相手が原因の中断: **両方が同時に出る。** これが4通りのうち
	// 最も落としやすい組み合わせで、「あなたの勝ち」と「相手のデータが
	// 不正でした」を同時に見せるために直交させてある（playstate.go の注釈）。
	// どちらか片方だけを出す実装にすると、勝ったのか事故なのか分からなくなる。
	lines = ResultDetail(s, "相手", "相手のデータが不正でした（hash-mismatch）", true, cols, maxLines)
	joined = strings.Join(lines, "\n")
	if !strings.Contains(joined, "相手が対戦から離れました") {
		t.Errorf("理由があると離脱の行が消えた: %q", lines)
	}
	if !strings.Contains(joined, "hash-mismatch") {
		t.Errorf("離脱の行があると理由が消えた: %q", lines)
	}

	// 普通の終わり方: 割合だけ。
	lines = ResultDetail(s, "CPU", "", false, cols, maxLines)
	if len(lines) != 1 || !strings.Contains(lines[0], "中立") {
		t.Errorf("普通の終わり方の行 = %q", lines)
	}
}

// TestResultDetailNeverExceedsMaxLines は、返す行数が maxLines を
// 決して超えないことを守る。
//
// **`maxLines - len(out)` が 0 になる引き算が罠である。** WrapText は
// 「maxLines が 0 以下なら行数を制限しない」と自分で定めているので、
// maxLines=1 と peerLeft=true が重なった瞬間に上限が消える。
// drawInfoLines は渡された行をそのまま描くので、情報欄からはみ出す。
// maxLines=3 だけで試すと 1+2=3 に収まってしまい、この枝を踏まない。
func TestResultDetailNeverExceedsMaxLines(t *testing.T) {
	var s rules.Score
	reasons := []string{
		"",
		"相手のデータが不正でした（hash-mismatch）",
		"接続できませんでした (setRemoteDescription failed: sdp: invalid syntax at line 3: unknown attribute ice-ufrag)",
	}
	for _, maxLines := range []int{1, 2, 3, 4} {
		for _, peerLeft := range []bool{false, true} {
			for _, reason := range reasons {
				for _, cols := range []int{8, 30} {
					lines := ResultDetail(s, "相手", reason, peerLeft, cols, maxLines)
					if len(lines) > maxLines {
						t.Errorf("maxLines=%d peerLeft=%v cols=%d reason=%.20q: %d 行 %q",
							maxLines, peerLeft, cols, reason, len(lines), lines)
					}
					for _, line := range lines {
						if n := len([]rune(line)); n > cols {
							t.Errorf("maxLines=%d cols=%d: 行が %d 文字 (%q)", maxLines, cols, n, line)
						}
					}
				}
			}
		}
	}
}

// TestResultSharesPutsMeFirstAndPairsWithSeat は、結果画面の
// 「あなた／相手」が席と正しく組んでいることを守る。
//
// **2つの Share() を入れ替えると、ゲストの画面だけで自分と相手の数字が
// 入れ替わる。** gfx にはこの環境でテストが書けないので、対応づけを
// ここで固定する。対戦画面の ScoreLabels と同じ理由である。
func TestResultSharesPutsMeFirstAndPairsWithSeat(t *testing.T) {
	for _, seat := range []rules.Player{rules.P1, rules.P2} {
		got := ResultShares(rules.Score{}, seat, "相手")
		if got[0].Label != NameYou {
			t.Errorf("席%v: 1つ目の見出し = %q, 期待 %q", seat, got[0].Label, NameYou)
		}
		if got[0].Player != seat {
			t.Errorf("席%v: 「あなた」が %v の割合を指している", seat, got[0].Player)
		}
		if got[1].Label != "相手" {
			t.Errorf("席%v: 2つ目の見出し = %q", seat, got[1].Label)
		}
		if got[1].Player != PeerOf(seat) {
			t.Errorf("席%v: 相手が %v の割合を指している（期待 %v）", seat, got[1].Player, PeerOf(seat))
		}
		if got[0].Player == got[1].Player {
			t.Errorf("席%v: 2つの区画が同じ席 %v を指している", seat, got[0].Player)
		}
	}
	// CPU 対戦では相手の呼び名が変わるだけで、対応づけは動かない。
	cpu := ResultShares(rules.Score{}, rules.P1, "CPU")
	if cpu[1].Label != "CPU" || cpu[1].Player != rules.P2 {
		t.Errorf("CPU 対戦の2つ目 = %+v", cpu[1])
	}
}

// TestResultSharesReadsTheRightNumbers は、見出しと割合の組が
// 実際の得点で入れ替わらないことを、値を入れて確かめる。
// 型の上で正しくても、gfx が sl.Player 以外から割合を引けば意味が無いので、
// ここでは ui 側の対応づけそのものを見る。
func TestResultSharesReadsTheRightNumbers(t *testing.T) {
	cfg := rules.DefaultConfig()
	b := rules.NewBoard(cfg)
	// P2 だけが濃い円を1つ置く。P2 の割合が P1 より大きくなる。
	b.Apply(rules.Circle{CX: 0.5, CY: 0.5, R: cfg.MinRadius()}, rules.P2, cfg.InkPerTurn)
	sc := b.Score()
	if sc.Share(rules.P2) <= sc.Share(rules.P1) {
		t.Fatalf("この仕立てでは P2 のほうが広いはず: P1=%v P2=%v", sc.Share(rules.P1), sc.Share(rules.P2))
	}

	// 席 P2（ゲスト）から見ると「あなた」のほうが広い。
	guest := ResultShares(sc, rules.P2, "相手")
	if sc.Share(guest[0].Player) <= sc.Share(guest[1].Player) {
		t.Errorf("席P2: 「あなた」の割合 %v が相手 %v より小さい",
			sc.Share(guest[0].Player), sc.Share(guest[1].Player))
	}
	// 席 P1（ホスト）から見ると「相手」のほうが広い。
	host := ResultShares(sc, rules.P1, "相手")
	if sc.Share(host[0].Player) >= sc.Share(host[1].Player) {
		t.Errorf("席P1: 「あなた」の割合 %v が相手 %v より大きい",
			sc.Share(host[0].Player), sc.Share(host[1].Player))
	}
}
