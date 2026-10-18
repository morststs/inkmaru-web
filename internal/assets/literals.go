package assets

import (
	"go/ast"
	"go/token"
	"strconv"
)

// stringLiterals はファイル中の文字列リテラルの中身を返す。
// 生文字列（backquote）も対象で、どちらも strconv.Unquote が解く。
func stringLiterals(f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			// 解けないリテラルは構文上ありえないが、解けたぶんだけで続ける。
			return true
		}
		out = append(out, s)
		return true
	})
	return out
}
