#!/bin/bash
# Inkmaru の画面を、画面の無い環境で撮る。
#
# ネイティブビルドを Xvfb（仮想ディスプレイ）の上で動かし、xdotool で操作して、
# Ebitengine 内蔵のスクリーンショット機能（EBITENGINE_SCREENSHOT_KEY）で保存する。
# 実際に動いているゲームを撮るので、手描きの模型ではなく本物の画面である。
#
# **これは WASM（ブラウザ）版の見た目の保証にはならない。** 描画コードは同じでも、
# ブラウザ側はキャンバスの拡大縮小・端末の画素密度・タッチ入力が絡む。
# ここで撮れるのは「描画ロジックが意図どおりか」までである。
#
# 要るもの: Xvfb・xdotool・cgo（gcc）・X11/GL の開発ヘッダと共有ライブラリ。
#   sudo apt-get install -y xvfb xdotool libx11-dev libgl1-mesa-dev
#
# 使い方:
#   ./scripts/screenshot.sh [出力先ディレクトリ]      # 既定は ./shots
set -eu
cd "$(dirname "$0")/.."
OUT=$(cd "${1:-shots}" 2>/dev/null && pwd || { mkdir -p "${1:-shots}"; cd "${1:-shots}"; pwd; })
DISP=":99"
W=390 H=844   # cmd/game の SetWindowSize と揃える

for c in Xvfb xdotool; do
  command -v "$c" >/dev/null || { echo "$c が要る（apt-get install xvfb xdotool）" >&2; exit 1; }
done

echo ">> ネイティブビルド"
BIN=$(mktemp -u /tmp/inkmaru-shot-XXXXXX)
go build -o "$BIN" ./cmd/game

export DISPLAY=$DISP
Xvfb $DISP -screen 0 480x900x24 >/dev/null 2>&1 & XPID=$!
sleep 2
cd "$OUT"
EBITENGINE_SCREENSHOT_KEY=q "$BIN" >"$OUT/game.log" 2>&1 & GPID=$!
sleep 5

# 装飾は無いが、ウィンドウは 0,0 ではなく Xvfb の 480px 幅（ゲームは 390px 幅）の
# 中央に置かれるので、画面の x = ゲーム内の x + 45 である（Task 7 で実測。
# drag 150→198 で描いた円の中心が、ゲーム内の x≈105 に出る）。y のずれは測っていない（未確認）。
# 押す場所は internal/ui のレイアウト計算と揃えること。
tap()  { xdotool mousemove --sync "$1" "$2" click 1; sleep "${3:-1}"; }
drag() { xdotool mousemove --sync "$1" "$2" mousedown 1; sleep 0.3
         xdotool mousemove --sync $(( ($1+$3)/2 )) $(( ($2+$4)/2 )); sleep 0.2
         xdotool mousemove --sync "$3" "$4"; sleep 0.3
         xdotool mouseup 1; sleep 0.5; }
shot() { xdotool key q; sleep 1.5
         local f; f=$(ls -t "$OUT"/screenshot_*.png 2>/dev/null | head -1)
         mv "$f" "$OUT/$1.png"; echo "   $1.png"; }

echo ">> 撮影"
shot title                                  # タイトル
tap 195 411 2                               # 「ふつう」で対戦開始
drag 150 330 198 330; shot turn1-preview    # 小さく濃い円を描いた状態
tap 195 814 4                               # 決定 → リビール演出
drag 240 470 345 470; shot turn2-overlap    # 相手の円に大きく薄い円を重ねた状態
tap 195 814 4
drag 120 500 200 500; shot turn3-preview
tap 195 814 5; shot result                  # 結果画面

kill $GPID $XPID 2>/dev/null || true
rm -f "$BIN" "$OUT/game.log"
echo ">> 完了: $OUT"
