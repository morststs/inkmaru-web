#!/bin/bash
# 画面に使うフォント（M PLUS Rounded 1c Medium）を、このゲームが表示する
# 文字だけに絞り込んで internal/assets に置く。
#
# 原本は 3.4 MB ある。丸ごと焼き込むと配信物（Brotli 後 4.24 MB）が3割増える
# ので、ソースの文字列リテラルに現れる文字＋印字可能な ASCII 全部に絞る。
# 実測（2026-10-10）: 原本 3,432,624 B → 絞り込み 約 81,000 B
# （Brotli 後 約 45,600 B、配信物の +1.1%）。
#
# **ASCII は使う分だけでなく U+0020-007E を全部入れる。** 相手から来る文字列
# （部屋番号）や pion のエラー文はこちらのソースに無く、何が出るか分からない。
#
# 何をいつ走らせるか:
#   日本語の文言を足したり変えたりしたら、これを走らせてフォントを作り直す。
#   忘れると internal/assets の TestEmbeddedFontCoversSourceRunes が落ちる
#   （落ちなければ、そのテストは何も守っていない）。
#
# 要るもの: python3 と fontTools。
#   pip3 install --user fonttools
#
# 使い方: ./scripts/subset-font.sh
set -eu
cd "$(dirname "$0")/.."

OUT=internal/assets/mplus-rounded-1c-medium-subset.ttf
# google/fonts の ofl/ 配下が原本。ライセンス（OFL-1.1）もここで決まっている。
SRC_URL=https://raw.githubusercontent.com/google/fonts/main/ofl/mplusrounded1c/MPLUSRounded1c-Medium.ttf

command -v python3 >/dev/null || { echo "python3 が要る" >&2; exit 1; }
python3 -c "import fontTools" 2>/dev/null || { echo "fontTools が要る（pip3 install --user fonttools）" >&2; exit 1; }

# 取ってきた原本は、他のものと混ざらないよう専用の空ディレクトリに置く。
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

echo ">> 原本を取得"
curl -sfL -o "$WORK/full.ttf" "$SRC_URL"
echo "   $(stat -c%s "$WORK/full.ttf") バイト"

echo ">> 字集合を数える（internal/assets.SourceRunes と同じ判断）"
go run ./internal/assets/cmd/charset -root . > "$WORK/chars.txt"
echo "   $(python3 -c "print(len(open('$WORK/chars.txt',encoding='utf-8').read()))") 字"

echo ">> 絞り込み"
python3 -m fontTools.subset "$WORK/full.ttf" \
  --text-file="$WORK/chars.txt" \
  --unicodes=U+0020-007E \
  --output-file="$OUT" \
  --layout-features='' \
  --no-hinting \
  --desubroutinize \
  --name-IDs='*' \
  --notdef-outline

echo ">> $OUT  $(stat -c%s "$OUT") バイト"
