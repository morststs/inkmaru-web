// charset は、このリポジトリの Go ソースの文字列リテラルに現れる文字を1行で出す。
// フォントの絞り込み（scripts/subset-font.sh）に渡すための道具である。
//
// 絞り込みと検査（internal/assets の font_test.go）が同じ関数
// assets.SourceRunes を使うことが要点で、2つが別々に字を数えると必ずずれる。
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/morststs/inkmaru-web/internal/assets"
)

func main() {
	root := flag.String("root", ".", "走査するディレクトリ")
	flag.Parse()
	rs, err := assets.SourceRunes(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "charset:", err)
		os.Exit(1)
	}
	fmt.Print(string(rs))
}
