package rtc

import "github.com/pion/webrtc/v4"

// Description は SDP を1つ表す。相手へそのまま運べる平叙な値である。
//
// pion の webrtc.SessionDescription をそのまま公開しないのは、この境界を
// 越えて pion の型が広がらないようにするためである。広がると
// internal/netplay や internal/signaling が pion を import することになり、
// 「netplay は pion を import しない」という約束を破るうえ、将来
// syscall/js の実装に差し替えるときの費用が跳ね上がる。
// 中身は文字列2つなので、変換はこのパッケージの中だけで完結する。
type Description struct {
	// Type は "offer" / "answer" / "pranswer" / "rollback"。
	// ブラウザの RTCSessionDescriptionInit と同じ綴りである。
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

// Candidate は ICE candidate を1つ表す。
// フィールド名と json タグはブラウザの RTCIceCandidateInit に合わせてある。
type Candidate struct {
	Candidate        string  `json:"candidate"`
	SDPMid           *string `json:"sdpMid,omitempty"`
	SDPMLineIndex    *uint16 `json:"sdpMLineIndex,omitempty"`
	UsernameFragment *string `json:"usernameFragment,omitempty"`
}

// toPion / fromPion は pion の型との変換。ここだけが pion の型に触れる。

func (d Description) toPion() webrtc.SessionDescription {
	return webrtc.SessionDescription{Type: webrtc.NewSDPType(d.Type), SDP: d.SDP}
}

func descriptionFromPion(sd webrtc.SessionDescription) Description {
	return Description{Type: sd.Type.String(), SDP: sd.SDP}
}

func (c Candidate) toPion() webrtc.ICECandidateInit {
	return webrtc.ICECandidateInit{
		Candidate:        c.Candidate,
		SDPMid:           c.SDPMid,
		SDPMLineIndex:    c.SDPMLineIndex,
		UsernameFragment: c.UsernameFragment,
	}
}

func candidateFromPion(ci webrtc.ICECandidateInit) Candidate {
	return Candidate{
		Candidate:        ci.Candidate,
		SDPMid:           ci.SDPMid,
		SDPMLineIndex:    ci.SDPMLineIndex,
		UsernameFragment: ci.UsernameFragment,
	}
}

// candidateKey は Candidate の同一性を比べるための鍵。
//
// Candidate 自体はポインタ場を持つので == で比べると番地の比較になり、
// 中身が同じでも別物と見なされてしまう。中身を写した比較可能な形にする。
type candidateKey struct {
	candidate string
	mid       string
	hasMid    bool
	index     uint16
	hasIndex  bool
	ufrag     string
	hasUfrag  bool
}

func (c Candidate) key() candidateKey {
	k := candidateKey{candidate: c.Candidate}
	if c.SDPMid != nil {
		k.mid, k.hasMid = *c.SDPMid, true
	}
	if c.SDPMLineIndex != nil {
		k.index, k.hasIndex = *c.SDPMLineIndex, true
	}
	if c.UsernameFragment != nil {
		k.ufrag, k.hasUfrag = *c.UsernameFragment, true
	}
	return k
}
