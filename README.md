# Inkmaru

2人のプレイヤーが同時に円を描いて盤面の陣地を奪い合う、ブラウザで遊ぶ対戦ゲーム。
Go + [Ebitengine](https://ebitengine.org/) で書き、WebAssembly として配信している。

**遊ぶ: https://inkmaru.e17.click**

このリポジトリは Inkmaru の**ブラウザ側（フロントエンド）のソースだけ**を公開したもの
である。オンライン対戦の仲介をするシグナリングサーバ（AWS 上で稼働）のコードと配備
設定は含まない。

## 遊び方

- 対戦は3ターン。各ターン、両プレイヤーは盤面に円を1つずつ描く。
- 円に使えるインクの量は毎ターン同じ。円を大きく描けばインクは薄く広く伸び、
  小さく描けば濃く狭く固まる。
- 円同士が重なった部分は、より濃い方の色が勝つ。濃さが同じ場合はどちらの
  色にもならず、中立（縞模様）になる。
- 3ターン終了時点で盤面をより多く陣地にしていたプレイヤーが勝ち。

CPU 対戦（やさしい／ふつう／つよい）と、部屋番号を使ったオンライン対戦がある。

## ビルドして手元で遊ぶ

```bash
./scripts/build-wasm.sh
python3 -m http.server 8080 --directory web
```

ブラウザで `http://localhost:8080` を開く（`file://` では WASM が読めない）。
`web/game.wasm` と `web/wasm_exec.js` が生成される。wasm 本体は大きい（約26 MB）ので、
配信するときは Brotli などで圧縮する前提である（圧縮後は約4.4 MB）。

オンライン対戦は、配備済みのシグナリングサーバ（接続先は `internal/gfx/scene_online.go`）
へ接続する。手元でビルドしたものからも同じサーバを使う。

## オンライン対戦の仕組み

- 部屋を作ると6文字の部屋番号が出る。それを相手に別の手段（口頭・チャットなど）で伝え、
  相手がその番号で入室する
- シグナリングサーバは SDP と ICE candidate を中継するだけで、WebRTC の P2P 接続
  （DataChannel）が確立した後の対戦の通信はサーバを経由しない
- WebRTC には [pion/webrtc](https://github.com/pion/webrtc) をそのまま使っている
  （`internal/rtc`）
- 同時に円を描くゲームなので、**commit-reveal** で「相手の手を見てから決める」不正を防ぎ、
  毎ターン盤面のハッシュを照合して食い違いを検出する（`internal/netplay`）
- **TURN には対応していない。** Symmetric NAT や一部の携帯回線では繋がらないことがあり、
  そのときは一定時間で諦めて CPU 対戦に戻れる

## 構成

| パッケージ | 役割 |
|---|---|
| `internal/rules` | ルール本体。盤面・陣地判定・ターン進行・勝敗 |
| `internal/ai` | CPU 3段階 |
| `internal/ui` | 表示ロジック。配色・レイアウト・入力変換・対戦の状態機械（`PlayState`） |
| `internal/gfx` | 描画と入力（Ebitengine に依存するのはここだけ） |
| `internal/netplay` | オンライン対戦のプロトコル。commit-reveal・盤面ハッシュ照合・制限時間 |
| `internal/rtc` | pion/webrtc を包み、「送受信できる1本の経路」だけを見せる |
| `internal/sigclient` | シグナリングサーバへの WebSocket クライアント |
| `internal/netwire` | 上の3つを配線する層 |
| `internal/signaling` | 部屋番号の書式（サーバと共有している部分だけ） |
| `internal/assets` | 焼き込むフォント（表示する文字だけに絞り込んだもの）と、その検査 |
| `cmd/game` | エントリポイント |

`internal/rules`・`internal/ai`・`internal/ui` は Ebitengine に依存しないので、
画面の無い環境でもテストできる。ゲームの流れを追うなら `internal/ui` の `PlayState` から
読むとよい。

ソースのコメントには、公開していない設計仕様書（`docs/...`）への参照が残っている。

## テスト

```bash
go test ./internal/rules/ ./internal/ai/ ./internal/ui/ ./internal/signaling/ ./internal/netplay/ ./internal/sigclient/ ./internal/rtc/ ./internal/netwire/ ./internal/assets/
```

画面は要らない。`go test ./...` はネイティブ版（`cmd/game`・`internal/gfx`）のビルドも
含むため、cgo と X11/GL の開発ヘッダが要る。

## ビルド（ネイティブデスクトップ・任意）

```bash
sudo apt-get install -y libgl1-mesa-dev libxcursor-dev libxi-dev libxinerama-dev libxrandr-dev libxxf86vm-dev
go build -o dist/inkmaru ./cmd/game
```

## 画面のフォント

画面の文字は M PLUS Rounded 1c Medium（SIL OFL-1.1）を、このゲームが表示する文字だけに
絞り込んで焼き込んでいる。文言を変えたら `./scripts/subset-font.sh` で作り直す。

## ライセンス

MIT License。詳細は [LICENSE](LICENSE) を参照。
依存ライブラリとフォントの帰属は [NOTICE](NOTICE)、ライセンス全文は
[LICENSE-THIRD-PARTY](LICENSE-THIRD-PARTY) にある。
