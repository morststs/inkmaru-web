#!/bin/sh
# Inkmaru を WebAssembly としてビルドし、web/ に配置するスクリプト。
set -eu
cd "$(dirname "$0")/.."
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" web/wasm_exec.js
GOOS=js GOARCH=wasm go build -o web/game.wasm ./cmd/game
ls -lh web/game.wasm
