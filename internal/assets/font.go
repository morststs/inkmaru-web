package assets

import _ "embed"

// MPlusRounded1cMedium は画面に使うフォント本体（TrueType）。
//
// 原本は M PLUS Rounded 1c Medium（SIL OFL-1.1、google/fonts の
// ofl/mplusrounded1c）で 3.4 MB あるが、ここに置いてあるのは
// **このゲームが表示する文字だけに絞り込んだもの**である
// （`scripts/subset-font.sh` が作る）。絞り込みの根拠は SourceRunes。
//
// 文言を足したら絞り込み直すこと。忘れた文字は豆腐にはならず、控えの
// ビットマップフォントで描かれる（internal/gfx/font.go の MultiFace）が、
// 字形がそこだけ古くなって目立つ。忘れたことは
// TestEmbeddedFontCoversSourceRunes が捕まえる。
//
//go:embed mplus-rounded-1c-medium-subset.ttf
var MPlusRounded1cMedium []byte
