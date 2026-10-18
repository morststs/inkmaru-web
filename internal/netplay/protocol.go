// Package netplay はオンライン対戦のプロトコルを実装する。
//
// このパッケージは通信ライブラリにも描画エンジンにも依存しない。
// Transport インタフェース越しにバイト列をやりとりするだけなので、
// 2つの Session をメモリ上でつなげば実通信なしに全経路を検証できる。
package netplay

// MsgType はやりとりするメッセージの種別。
type MsgType string

const (
	MsgCommit MsgType = "commit"
	MsgReveal MsgType = "reveal"
	MsgBoard  MsgType = "board"
	MsgAbort  MsgType = "abort"
	// MsgRematch は「同じ相手ともう一度」の申し出。両者が1通ずつ送ったときだけ
	// 再戦が成立する（再戦の設計 §2）。
	MsgRematch MsgType = "rematch"
)

// 中断の理由。
const (
	ReasonHashMismatch  = "hash-mismatch"
	ReasonBoardMismatch = "board-mismatch"
	ReasonProtocol      = "protocol"
)

// Message は1通のメッセージ。
//
// Turn を全種別に載せているのは、シグナリングを介した経路では到着順が
// 保たれないためである（第2段階a §11 で実測）。どのターンの手かを
// 受け取った順序で判断すると取り違える。
type Message struct {
	T    MsgType `json:"t"`
	Turn int     `json:"turn"`

	Hash string `json:"hash,omitempty"` // commit と board で使う

	CX    float64 `json:"cx,omitempty"` // reveal で使う
	CY    float64 `json:"cy,omitempty"`
	R     float64 `json:"r,omitempty"`
	Nonce string  `json:"nonce,omitempty"`

	Reason string `json:"reason,omitempty"` // abort で使う
}
