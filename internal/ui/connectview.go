package ui

// ConnPhase はオンライン接続の組み立てが今どこにいるかを、
// 表示に必要な粒度だけで表したもの。
//
// **`internal/netplay.ConnectState` をそのまま受け取ることはできない。**
// `internal/netplay` は `ErrPeerGone`・`PeerFault` のために `internal/ui` を
// import しており（`netopponent.go`）、依存の向きは netplay → ui の一方向である。
// ここで netplay を import すると import 環になる。だから表示側の語彙を
// このパッケージに置き、netplay の状態からの変換は両方を見てよい層
// （`internal/netwire`。`UIPhase` と `Handle.Phase`）に置いて、そちらでテストする。
type ConnPhase int

const (
	// ConnIdle はまだ何も始めていない状態。画面は部屋番号の入力欄を出す。
	ConnIdle ConnPhase = iota
	// ConnCreating は部屋の作成を頼んで応答を待っている状態。
	ConnCreating
	// ConnWaitingPeer は部屋ができて相手の参加を待っている状態。
	ConnWaitingPeer
	// ConnConnecting は相手が居ることが分かり P2P の確立を進めている状態。
	ConnConnecting
	// ConnReady は対戦を始められる状態。画面はこの値を表示に使わない
	// （その場で対戦画面へ移る）。
	ConnReady
	// ConnFailed は組み立てを諦めた状態。理由を添えて戻る導線を出す。
	ConnFailed
)

// ConnectStatus は接続の組み立て中に画面へ出す文言を返す。
// head は大きく1行、detail は情報欄に折り返して並べる複数行である。
//
// **`ConnCreating` に専用の文言を持たせることが、この関数がある一番の理由である。**
// 仕様書 §8.3 が名指しで要求している。`create` の応答はコールド時に1.4秒を
// 超える（第2段階a §11 の実測）ので、押した瞬間に番号が出る前提の画面にすると
// 「反応していない」と誤解され、連打されることになる。この文言が消えていないことを
// go test で確かめられるように、対応表を gfx ではなくここに置いている。
//
// failReason には pion 由来の長い英文が混ざりうるので、cols/maxLines で
// 折り返して切り詰める。
func ConnectStatus(p ConnPhase, room, failReason string, cols, maxLines int) (head string, detail []string) {
	switch p {
	case ConnCreating:
		return "部屋を作成中…", []string{"しばらくかかることがあります"}
	case ConnWaitingPeer:
		if room == "" {
			// 番号がまだ手元に無い。ここで空の見出しを返すと、画面が
			// 「何も起きていない」ように見えて押し直しを誘う。
			return "部屋を作成中…", []string{"しばらくかかることがあります"}
		}
		// 番号は口頭やチャットで相手に伝えるものなので、3桁ずつ区切って大きく出す。
		return FormatRoomCode(room), []string{"相手の参加を待っています"}
	case ConnConnecting:
		return "接続中…", nil
	case ConnFailed:
		return "接続できませんでした", WrapText(failReason, cols, maxLines)
	case ConnReady:
		return "対戦を始めます", nil
	default:
		return "準備中…", nil
	}
}
