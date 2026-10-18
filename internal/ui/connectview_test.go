package ui

import (
	"strings"
	"testing"
)

// allConnPhases は ConnectStatus が扱うべき状態の全部。
// 増えたときにここも増やすこと（default に落ちて「準備中…」になる）。
var allConnPhases = []ConnPhase{
	ConnIdle, ConnCreating, ConnWaitingPeer, ConnConnecting, ConnReady, ConnFailed,
}

// TestConnectStatusHasCreatingMessage は仕様書 §8.3 が名指しで要求する
// 「部屋を作成中…」が存在し、他のどの状態とも違う文言であることを守る。
//
// **create の応答はコールド時に1.4秒を超える**（第2段階a §11 の実測）。
// 押した瞬間に番号が出る前提の画面にすると「反応していない」と誤解され、
// 連打される。この文言を消したり、他の状態と同じ文言にしたりしてはならない。
func TestConnectStatusHasCreatingMessage(t *testing.T) {
	head, detail := ConnectStatus(ConnCreating, "", "", 30, 3)
	if head != "部屋を作成中…" {
		t.Errorf("ConnCreating の見出し = %q, 期待 %q", head, "部屋を作成中…")
	}
	if len(detail) == 0 {
		t.Error("ConnCreating に待たせる説明が無い")
	}
	// 他の状態と見分けがつくこと。同じ文言に潰れると、押しても
	// 何も起きていないように見える状態が生まれる。
	for _, p := range allConnPhases {
		if p == ConnCreating {
			continue
		}
		other, _ := ConnectStatus(p, "P2Z5BS", "理由", 30, 3)
		if other == head {
			t.Errorf("%v の見出しが ConnCreating と同じ %q", p, head)
		}
	}
}

// TestConnectStatusShowsFormattedRoomCode は、待機中に3桁区切りの
// 部屋番号が出ることを守る。番号は口頭やチャットで人が伝える。
func TestConnectStatusShowsFormattedRoomCode(t *testing.T) {
	head, detail := ConnectStatus(ConnWaitingPeer, "P2Z5BS", "", 30, 3)
	if head != "P2Z-5BS" {
		t.Errorf("待機中の見出し = %q, 期待 %q", head, "P2Z-5BS")
	}
	if head != FormatRoomCode("P2Z5BS") {
		t.Errorf("FormatRoomCode を通していない: %q", head)
	}
	if strings.Join(detail, "") == "" {
		t.Error("待機中であることの説明が無い")
	}
}

// TestConnectStatusWrapsFailReason は、失敗理由が画面の桁数に収まることを守る。
// pion 由来の長い英文が1行に収まる前提で書かれていると、右端を突き抜ける。
func TestConnectStatusWrapsFailReason(t *testing.T) {
	const reason = "接続できませんでした (setRemoteDescription failed: sdp: invalid syntax at line 3: unknown attribute ice-ufrag)"
	for _, cols := range []int{8, 20, 30} {
		for _, maxLines := range []int{1, 3} {
			head, detail := ConnectStatus(ConnFailed, "", reason, cols, maxLines)
			if head == "" {
				t.Errorf("失敗の見出しが空")
			}
			if len(detail) == 0 {
				t.Errorf("cols=%d max=%d: 理由が出ていない", cols, maxLines)
			}
			if len(detail) > maxLines {
				t.Errorf("cols=%d max=%d: %d 行出た", cols, maxLines, len(detail))
			}
			for _, line := range detail {
				if n := len([]rune(line)); n > cols {
					t.Errorf("cols=%d max=%d: 行が %d 文字 (%q)", cols, maxLines, n, line)
				}
			}
		}
	}
}

// TestConnectStatusNeverEmptyHead は、どの状態でも見出しが空にならないことを守る。
// 空だと「何も起きていない画面」に見え、押し直しや連打を誘う。
func TestConnectStatusNeverEmptyHead(t *testing.T) {
	for _, p := range allConnPhases {
		if head, _ := ConnectStatus(p, "", "", 30, 3); head == "" {
			t.Errorf("%v の見出しが空", p)
		}
	}
}
