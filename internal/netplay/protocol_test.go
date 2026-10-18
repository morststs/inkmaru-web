package netplay

import (
	"encoding/json"
	"testing"
)

func TestMessageRoundTrip(t *testing.T) {
	in := Message{T: MsgReveal, Turn: 2, CX: 0.25, CY: 0.75, R: 0.15, Nonce: "abcd"}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out Message
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out != in {
		t.Errorf("往復で変化した: %+v -> %+v", in, out)
	}
}

// turn を全メッセージに載せるのは、到着順が保たれない経路で
// どのターンの手か取り違えないためである（仕様書 §4）。
func TestEveryMessageCarriesTurn(t *testing.T) {
	for _, m := range []Message{
		{T: MsgCommit, Turn: 1, Hash: "x"},
		{T: MsgReveal, Turn: 1},
		{T: MsgBoard, Turn: 1, Hash: "y"},
	} {
		b, _ := json.Marshal(m)
		var back map[string]any
		json.Unmarshal(b, &back)
		if _, ok := back["turn"]; !ok {
			t.Errorf("%v に turn が入っていない: %s", m.T, b)
		}
	}
}

func TestAbortOmitsUnusedFields(t *testing.T) {
	b, _ := json.Marshal(Message{T: MsgAbort, Reason: ReasonHashMismatch})
	if string(b) != `{"t":"abort","turn":0,"reason":"hash-mismatch"}` {
		t.Errorf("出力 = %s", b)
	}
}
