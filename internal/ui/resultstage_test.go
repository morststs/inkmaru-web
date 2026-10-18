package ui

import (
	"testing"
	"time"
)

// 結果画面は段階を踏んで出す。盤面 → 帯 → 数字 → 勝敗。
func TestResultStageAt(t *testing.T) {
	cases := []struct {
		elapsed time.Duration
		want    ResultStage
	}{
		{0, StageBoard},
		{60 * time.Millisecond, StageBoard},
		{120 * time.Millisecond, StageBar},
		{200 * time.Millisecond, StageBar},
		{240 * time.Millisecond, StageNumbers},
		{360 * time.Millisecond, StageMessage},
		{10 * time.Second, StageMessage},
	}
	for _, c := range cases {
		if got := ResultStageAt(c.elapsed); got != c.want {
			t.Errorf("ResultStageAt(%v) = %v, 期待 %v", c.elapsed, got, c.want)
		}
	}
}

// 段階は巻き戻らない。
//
// **タブが裏に回ると dt が大きく飛ぶ。** 経過時間を剰余で折り返すような
// 実装にすると、戻ってきた瞬間に勝敗が消えて盤面だけに戻る。
func TestResultStageNeverGoesBackwards(t *testing.T) {
	prev := ResultStageAt(0)
	for ms := 0; ms <= 60000; ms += 7 {
		got := ResultStageAt(time.Duration(ms) * time.Millisecond)
		if got < prev {
			t.Fatalf("%dms で %v へ巻き戻った（直前は %v）", ms, got, prev)
		}
		prev = got
	}
}

// 負の経過時間でも最初の段階を返す（時計が巻き戻っても画面が壊れない）。
func TestResultStageWithNegativeElapsed(t *testing.T) {
	if got := ResultStageAt(-time.Second); got != StageBoard {
		t.Errorf("ResultStageAt(-1s) = %v, 期待 %v", got, StageBoard)
	}
}
