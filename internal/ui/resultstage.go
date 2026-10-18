package ui

import "time"

// ResultStage は結果画面がどこまで出ているか。
//
// **順に大きくなる。** 段階の比較（got < prev）で巻き戻りを検査できるように
// してある。
type ResultStage int

const (
	// StageBoard は盤面だけが出ている段階。
	StageBoard ResultStage = iota
	// StageBar は帯グラフまで出ている段階。
	StageBar
	// StageNumbers は割合の数字まで出ている段階。
	StageNumbers
	// StageMessage は勝敗の文言まで出ている段階。ここで止まる。
	StageMessage
)

// stageStep は1段階ぶんの間。
const stageStep = 120 * time.Millisecond

// ResultStageAt は結果画面が始まってからの経過時間で、出す段階を返す。
//
// **折り返してはならない。** タブが裏に回ると経過時間は大きく飛ぶ。
// 剰余を取る実装にすると、戻ってきた瞬間に勝敗が消える。
func ResultStageAt(elapsed time.Duration) ResultStage {
	if elapsed < 0 {
		return StageBoard
	}
	n := ResultStage(elapsed / stageStep)
	if n > StageMessage {
		return StageMessage
	}
	return n
}
