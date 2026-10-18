// Package assets は配布物に焼き込む素材と、その素材で足りているかを
// 検査する手段を持つ。
//
// **Ebitengine には依存させない。** 「フォントに字が足りているか」は画面の
// 有無と無関係な判断なので、ここに置けば `go test` だけで検証できる
// （CLAUDE.md「設計上の約束」）。
package assets

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// SourceRunes は root 配下の Go ソースの文字列リテラルに現れる文字を、
// 重複なく昇順で返す。テストファイル（`_test.go`）は見ない。
//
// 埋め込むフォントはこの集合に絞り込む。**ここで漏らした文字は、画面から
// 消えるのではなく控えのビットマップフォントで描かれる**（font.go の
// MultiFace）。豆腐にはならないが、字形が1文字だけ古くなって目立つ。
//
// だから**何を集めるかで判断をしない。** 表示に使われない文字列——JSON の
// キー、サーバ側のログ、エラーの原因——も区別せず全部入れる。ブラウザ配布物
// に入るパッケージだけに絞っても差は17字しかなく、絞り込んだフォントの
// 大きさはほとんど変わらない（2026-10-10 実測、200字 81KB に対し183字）。
// 一方で「これは表示されない」と決めつけて外した文字が実は表示される、
// という失敗は誰にも気づかれずに出荷される。
//
// 字形の要らない文字（改行・タブなどの制御文字）は除く。
func SourceRunes(root string) ([]rune, error) {
	seen := map[rune]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// 起点そのものは、名前が "." や ".." でも必ず降りる。
			// ここを名前だけで弾くと、SourceRunes("../..") が何も集めずに
			// 成功してしまう（テストは緑のまま、フォントの検査が消える）。
			if path == root {
				return nil
			}
			// 生成物や外部から持ち込んだソース、作業用の捨て場は見ない。
			//
			// **tmp と shots を外すのが要点である。** どちらも .gitignore に
			// 載っている捨て場で、その場かぎりの Go ファイルが置かれることが
			// ある。拾うと、焼き込むフォントが追跡されていないファイルに
			// 左右され、他の人の手元で作り直したときに一致しなくなる。
			switch d.Name() {
			case "vendor", "testdata", "node_modules", "tmp", "shots":
				return fs.SkipDir
			}
			if strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		// go/parser を使うのは、コメントを拾わないためである。このリポジトリの
		// コメントは日本語で、しかも本文より長い。正規表現で `"..."` を拾うと
		// コメント中の引用まで入り、絞り込みの意味が薄れる。
		for _, lit := range stringLiterals(f) {
			for _, r := range lit {
				if unicode.IsPrint(r) {
					seen[r] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]rune, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}
