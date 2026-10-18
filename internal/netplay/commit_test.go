package netplay

import (
	"crypto/rand"
	"testing"

	"github.com/morststs/inkmaru-web/internal/rules"
)

func TestCommitHashIsStable(t *testing.T) {
	nonce := make([]byte, NonceLen)
	rand.Read(nonce)
	c := rules.Circle{CX: 0.3456789012345, CY: 0.5, R: 0.2}
	if CommitHash(c, nonce) != CommitHash(c, nonce) {
		t.Fatal("同じ入力から異なるハッシュが出た")
	}
	if len(CommitHash(c, nonce)) != 64 {
		t.Errorf("ハッシュの長さ = %d, 期待 64（SHA-256 の hex）", len(CommitHash(c, nonce)))
	}
}

// 円をわずかに変えたら別のハッシュになること。これが無いと commit に拘束力が無い。
func TestCommitHashIsBinding(t *testing.T) {
	nonce := make([]byte, NonceLen)
	rand.Read(nonce)
	base := rules.Circle{CX: 0.3456789012345, CY: 0.5, R: 0.2}
	cases := []rules.Circle{
		{CX: 0.3456789012346, CY: 0.5, R: 0.2},
		{CX: 0.3456789012345, CY: 0.5000000000001, R: 0.2},
		{CX: 0.3456789012345, CY: 0.5, R: 0.2000000000001},
	}
	for _, c := range cases {
		if CommitHash(base, nonce) == CommitHash(c, nonce) {
			t.Errorf("%v と %v のハッシュが同じ", base, c)
		}
	}
}

// nonce が違えば同じ円でも別のハッシュ。総当たり耐性の根拠。
func TestCommitHashDependsOnNonce(t *testing.T) {
	c := rules.Circle{CX: 0.5, CY: 0.5, R: 0.2}
	a, b := make([]byte, NonceLen), make([]byte, NonceLen)
	rand.Read(a)
	rand.Read(b)
	if CommitHash(c, a) == CommitHash(c, b) {
		t.Error("nonce を変えてもハッシュが同じ")
	}
}

func TestNewNonceLengthAndVariety(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		n, err := NewNonce()
		if err != nil {
			t.Fatalf("NewNonce: %v", err)
		}
		if len(n) != NonceLen {
			t.Fatalf("長さ = %d, 期待 %d", len(n), NonceLen)
		}
		seen[string(n)] = true
	}
	if len(seen) != 100 {
		t.Errorf("100回中 %d 種類しか出なかった（乱数が壊れている疑い）", len(seen))
	}
}

func TestBoardHashDetectsDifference(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 64
	a := rules.NewBoard(cfg)
	a.Apply(rules.Circle{CX: 0.3, CY: 0.7, R: 0.2}, rules.P1, cfg.InkPerTurn)

	same := rules.NewBoard(cfg)
	same.Apply(rules.Circle{CX: 0.3, CY: 0.7, R: 0.2}, rules.P1, cfg.InkPerTurn)
	if BoardHash(a) != BoardHash(same) {
		t.Error("同じ手順の盤面で異なるハッシュが出た")
	}

	// 所有者が1マスでも違えば別のハッシュになること。
	diff := rules.NewBoard(cfg)
	diff.Apply(rules.Circle{CX: 0.3, CY: 0.7, R: 0.2}, rules.P2, cfg.InkPerTurn)
	if BoardHash(a) == BoardHash(diff) {
		t.Error("所有者が違う盤面で同じハッシュが出た")
	}

	// 反復順が固定されていることを確認する。
	// x-y を入れ替えた円は非対称な盤面では異なるハッシュを生む。
	mirror := rules.NewBoard(cfg)
	mirror.Apply(rules.Circle{CX: 0.7, CY: 0.3, R: 0.2}, rules.P1, cfg.InkPerTurn)
	if BoardHash(a) == BoardHash(mirror) {
		t.Error("反復順が行優先でない可能性：x-y 交換後でもハッシュが同じ")
	}
}

// 符号化方式そのものを固定する。差分的な性質を見るテストは、符号化を
// 文字列化に変えても通ってしまう（%v は最短往復表現なので、摂動させた値でも
// 別の文字列になる）。仕様書 §5.1 が float64 のビット表現を固定長
// ビッグエンディアンで並べると決めたのは、桁数や指数表記が環境で揺れると
// 両者のハッシュが食い違い、正常な対戦がすべて「相手のデータが不正」で
// 中断してしまうためである。その決定をここで固定する。
//
// この期待値を更新してよいのは、符号化を意図的に変えるときだけである。
func TestCommitHashGoldenVector(t *testing.T) {
	c := rules.Circle{CX: 0.25, CY: 0.5, R: 0.125}
	nonce := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	const want = "e1ce2ee92e35bca0d3782c1447b676c2032d8986dfe296ec054d9a3f053fb100"
	if got := CommitHash(c, nonce); got != want {
		t.Errorf("CommitHash = %s, 期待 %s（符号化方式が変わった可能性がある）", got, want)
	}
}

// 符号化方式そのものを固定する。BoardHash についても同様。
func TestBoardHashGoldenVector(t *testing.T) {
	cfg := rules.DefaultConfig()
	cfg.GridN = 8
	b := rules.NewBoard(cfg)
	b.Apply(rules.Circle{CX: 0.5, CY: 0.5, R: 0.25}, rules.P1, cfg.InkPerTurn)
	const want = "5b76f36c1b88fd261ce6db904bad7f03711635a92b88b10e39b73bd9b3a179a2"
	if got := BoardHash(b); got != want {
		t.Errorf("BoardHash = %s, 期待 %s（符号化方式が変わった可能性がある）", got, want)
	}
}
