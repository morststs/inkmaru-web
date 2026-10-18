package assets

import (
	"bytes"
	"testing"

	"github.com/go-text/typesetting/font"
)

// 埋め込んだフォントが、ソースに現れる文字を全部持っていること。
//
// **これが絞り込みの唯一の安全網である。** 文言を足して
// `scripts/subset-font.sh` を走らせ忘れると、その文字だけ控えのビットマップ
// フォントで描かれ、字形が揃わない。画面を見れば分かるが、オンライン対戦の
// 失敗時の文言のように**普段出ない画面**だと誰も気づかない。
func TestEmbeddedFontCoversSourceRunes(t *testing.T) {
	face := parseEmbedded(t)
	rs, err := SourceRunes("../..")
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) < 100 {
		t.Fatalf("走査が効いていない（%d字しか集まっていない）", len(rs))
	}
	var missing []rune
	for _, r := range rs {
		if _, ok := face.NominalGlyph(r); !ok {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		t.Errorf("フォントに無い文字が %d 字ある: %q\n"+
			"./scripts/subset-font.sh を走らせて絞り込み直すこと", len(missing), string(missing))
	}
}

// 印字可能な ASCII は、ソースに出てこなくても全部持っていること。
//
// **相手から来る文字列はこちらのソースに無い。** 部屋番号も、接続に失敗した
// ときに pion が返す英文も、何の記号が混ざるか分からない。ソースにある分だけ
// 絞ると、そこだけ字形が崩れる。
func TestEmbeddedFontCoversPrintableASCII(t *testing.T) {
	face := parseEmbedded(t)
	var missing []rune
	for r := rune(0x20); r <= 0x7E; r++ {
		if _, ok := face.NominalGlyph(r); !ok {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		t.Errorf("ASCII に欠けがある: %q", string(missing))
	}
}

func parseEmbedded(t *testing.T) *font.Face {
	t.Helper()
	f, err := font.ParseTTF(bytes.NewReader(MPlusRounded1cMedium))
	if err != nil {
		t.Fatalf("埋め込んだフォントが読めない: %v", err)
	}
	return f
}
