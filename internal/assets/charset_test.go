package assets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SourceRunes が何を拾い、何を拾わないかを固定する。
//
// **コメントを拾わないことが要である。** このリポジトリのコメントは日本語で
// 本文より長いので、正規表現で `"..."` を拾う実装に戻すと、絞り込みの対象が
// 一気に膨らんで「絞り込む」意味が消える。
func TestSourceRunesTakesLiteralsOnly(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package a\n"+
		"// 注釈にだけ出る字: 彗\n"+
		"const S = \"表示される\"\n"+
		"const R = `生文字列も拾う: 雫`\n"+
		"const N = \"改行は要らない\\n\"\n")
	write("a_test.go", "package a\nconst T = \"試験にだけ出る字: 黴\"\n")
	write("sub/b.go", "package b\nconst U = \"入れ子も見る: 鰹\"\n")
	write("testdata/c.go", "package c\nconst V = \"testdata は見ない: 鬱\"\n")

	rs, err := SourceRunes(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := string(rs)
	for _, c := range []string{"表", "示", "さ", "れ", "る", "雫", "鰹"} {
		if !strings.Contains(got, c) {
			t.Errorf("拾えていない: %q (結果 %q)", c, got)
		}
	}
	for name, c := range map[string]string{
		"コメント":     "彗",
		"テストファイル":  "黴",
		"testdata": "鬱",
		"改行":       "\n",
	} {
		if strings.Contains(got, c) {
			t.Errorf("%sから拾ってしまった: %q (結果 %q)", name, c, got)
		}
	}
}

// 結果は重複なく昇順である。絞り込みの道具にそのまま渡すため。
func TestSourceRunesIsSortedAndUnique(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("package a\nconst S = \"ばばば ああ ばば\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rs, err := SourceRunes(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(rs); i++ {
		if rs[i-1] >= rs[i] {
			t.Fatalf("昇順でないか重複がある: %q", string(rs))
		}
	}
}
