package ui

import (
	"fmt"

	"github.com/morststs/inkmaru-web/internal/rules"
)

// NameYou は自分を指す呼び名。相手の呼び名は対戦の種類で変わる（CPU／相手）ので
// 引数で受け取る。
const NameYou = "あなた"

// NameNeutral は中立地帯の呼び名。
const NameNeutral = "中立"

// PeerOf は相手の席を返す。P1・P2 以外を渡した場合は P2 の相手として P1 を返す。
func PeerOf(p rules.Player) rules.Player {
	if p == rules.P1 {
		return rules.P2
	}
	return rules.P1
}

// SeatLabel は帯グラフの区画1つ。色は Player から引く。
type SeatLabel struct {
	Player rules.Player
	Label  string
}

// ScoreLabels は帯グラフの区画を **盤面の順（P1・中立・P2）で** 返す。
//
// **順序を席で入れ替えてはならない。** 同じ盤面が2つの画面で違う並び・違う色に
// 見えると、相手と番号や状況を口で伝え合えなくなる。席によって変わるのは
// 「どちらが『あなた』か」だけである。
//
// この対応を gfx ではなく ui に置いてあるのは、ここが第2段階で最も高くついた
// 欠陥（自分を常に P1 と決め打ちする）の再発しうる場所であり、gfx には
// この開発環境でテストが書けないからである。ゲストの席は P2 なので、
// 決め打ちのままだとゲストの画面では自分と相手の割合が入れ替わり、
// 勝ったのに「相手の勝ち」と出る。
func ScoreLabels(seat rules.Player, peerName string) [3]SeatLabel {
	me, peer := NameYou, peerName
	if seat == rules.P2 {
		me, peer = peerName, NameYou
	}
	return [3]SeatLabel{
		{Player: rules.P1, Label: me},
		{Player: rules.Neutral, Label: NameNeutral},
		{Player: rules.P2, Label: peer},
	}
}

// ResultShares は結果画面の「あなた X%%／相手 Y%%」の見出しと割合を、
// **自分が先** の順で返す。
//
// ScoreLabels（対戦画面の帯グラフ）と違って盤面の順に固定しない。帯グラフは
// 色と面積の並びが2つの画面で一致していなければならないが、結果画面の1行は
// 図ではなく文である。「あなた」を先に読ませるほうが自然で、順序が両画面で
// 違っても盤面の見え方は変わらない。
//
// **見出しと割合の対応づけを gfx で手組みさせないことが目的である。**
// 2つの Share() を入れ替えても gfx にはテストが1行も書けないので、
// ゲストの画面で自分と相手の数字が入れ替わっても誰も気づかない。
// これは第2段階で最も高くついた欠陥と同じ形である。
func ResultShares(s rules.Score, seat rules.Player, peerName string) [2]SeatLabel {
	return [2]SeatLabel{
		{Player: seat, Label: NameYou},
		{Player: PeerOf(seat), Label: peerName},
	}
}

// ResultMessage は勝敗の文言を自分の席から決める。
// peerName は相手の呼び名（CPU 対戦なら "CPU"、オンラインなら "相手"）。
//
// peerLeft が真なら盤面の集計を **上書きして** 自分の勝ちにする。
// これは描画の都合ではなく仕様書 §5.4 のゲーム規則である。commit が揃ったあと、
// 先に実体を送った側の情報を見てから「不利だから切断する」ことは技術的に
// 防げないので、逃げても勝てないようにして動機を消す。盤面の数字に
// 上書きさせると、その規則が消える。
//
// res が rules.InProgress なのは、3ターン戦い切らずに中断した対戦である。
// **これを「引き分け」と呼んではならない。**「3ターン戦って同点だった」と
// 読めてしまう。勝敗がついていないことと、途中で終わったことは別である。
func ResultMessage(res rules.Result, seat rules.Player, peerName string, peerLeft bool) string {
	if peerLeft {
		return NameYou + "の勝ち"
	}
	switch res {
	case rules.WinP1:
		if seat == rules.P1 {
			return NameYou + "の勝ち"
		}
		return peerName + "の勝ち"
	case rules.WinP2:
		if seat == rules.P2 {
			return NameYou + "の勝ち"
		}
		return peerName + "の勝ち"
	case rules.Draw:
		return "引き分け"
	default:
		return "勝敗なし（中断）"
	}
}

// ResultDetail は結果画面の情報欄に並べる行を組み立てる。
//
// **中断の理由を切り詰めてはならない。** 理由の値打ちは末尾の識別子
// （hash-mismatch・board-mismatch など）にあり、そこが「…」で落ちると
// 表示する意味が無くなる。だから理由があるときは中立・未取得の割合を譲る。
// 中断のときは割合より識別子を優先する、という取引である。
//
// 注意: 割合が他のどこかに出ているからではない。**結果画面に帯グラフは無く、
// 中立と未取得の数字はここ以外に出ない**（中立の縞は盤面の画像として見えるだけ）。
// 帯グラフがあるのは対戦画面のほうである。理由があるときは割合が
// 「どこにも出なくなる」ことを承知のうえでの取引だと読むこと。
func ResultDetail(s rules.Score, peerName, abortReason string, peerLeft bool, cols, maxLines int) []string {
	if maxLines < 1 {
		maxLines = 1
	}
	var out []string
	// add は1文を折り返して、残りの行数の範囲だけ足す。
	//
	// **どの行も cols と maxLines の両方を守らせるために、必ずここを通す。**
	// 一部の行だけ WrapText を通さない実装にしていたせいで、狭い画面
	// （200x320 では14桁）で離脱の行と割合の行がはみ出していた。
	// また残り行数が 0 のまま WrapText を呼んではならない。WrapText は
	// 「maxLines が 0 以下なら制限しない」と定めているので、そこで上限が消える。
	//
	// **行数の歯止めはここ1つだけにしてある。** 最後に out[:maxLines] で
	// 切る保険も足していたが、それがあると引き算を壊す変異が保険に隠れて
	// テストで検出できなくなる（実測で3件が生き残った）。二重の防御は、
	// 片方が壊れたことを誰も知らせなくなるという代償を伴う。
	// add が毎回残り行数を上限に渡すので、len(out) が maxLines を超える経路は無い。
	add := func(line string) {
		n := maxLines - len(out)
		if n < 1 {
			return
		}
		out = append(out, WrapText(line, cols, n)...)
	}

	// PeerLeft() と AbortReason() は直交する（playstate.go の注釈を読むこと）。
	// 素の切断では前者だけ、相手が原因の中断では両方、自分側の失敗では
	// 後者だけが立つ。だからどちらも独立に見る。
	if peerLeft {
		add(peerName + "が対戦から離れました")
	}
	if abortReason != "" {
		add("理由: " + abortReason)
	} else {
		add(fmt.Sprintf("中立 %.1f%%   未取得 %.1f%%",
			100*s.Share(rules.Neutral), 100*s.Share(rules.None)))
	}
	return out
}
