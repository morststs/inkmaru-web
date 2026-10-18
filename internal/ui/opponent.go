package ui

import (
	"errors"
	"math/rand"
	"time"

	"github.com/morststs/inkmaru-web/internal/ai"
	"github.com/morststs/inkmaru-web/internal/rules"
)

// ErrPeerGone は相手が対戦から消えたことを表す番兵エラー。
//
// 仕様書 §5.4・§7.2 のとおり、切断は切断した側の負けとして扱う。
// commit が揃ったあと、先に実体を送った側の情報を見てから「不利だから切断する」
// ことは技術的に防げないため、逃げても勝てないようにして動機を消す。
//
// この定数を internal/netplay ではなく internal/ui に置いているのは、
// internal/ui が internal/netplay を import しないという約束のためである。
// 依存の向きは netplay → ui の一方向で、オンラインの Opponent 実装が
// 自分側のエラーをこの番兵で包んで返す。
var ErrPeerGone = errors.New("ui: 相手が対戦から離れた")

// PeerFault は「相手側の落ち度で対戦が終わった」ことと、その理由を表す。
//
// 切断と、相手が原因の中断（相手が abort を通告してきた／相手のデータが
// 不正だった）はどちらも切断した側・不正な側の負けとして扱う（仕様書 §5.4）。
// ただし結果画面には「誰が勝ったか」だけでなく「なぜ終わったか」も出したいので、
// 勝敗の分類（ErrPeerGone を包んでいること）とは別に理由を運ぶ。
//
// 実装は internal/netplay 側にある。ui は理由の文字列を表示するだけで、
// 中身を解釈しない。
type PeerFault interface {
	error
	// PeerFaultReason は終わった理由。空なら理由なしの素の切断である。
	PeerFaultReason() string
}

// Opponent は相手の手を得る手段。CPU とオンラインを差し替える。
//
// Submit と Poll に分かれているのは、ゲームループを止めないためである。
// Ebitengine の Update() は毎フレーム呼ばれるので、そこから呼ばれる処理が
// 相手を待つと画面が固まる。相手の返答が数フレーム後になっても、Poll は
// そのつど ok=false で即座に戻る。
//
// Submit に渡す円は「確定済み」の円だけである。PlayState は Confirm() の
// 中でしか Submit を呼ばず、プレビュー中の円を Opponent に見せることはない。
// この不変条件が同時進行の前提を支えている。
type Opponent interface {
	// Submit は自分の手を相手に伝える。ブロックしてはならない。
	Submit(mine rules.Circle, turn int) error
	// Poll は相手の手が確定していれば返す。まだなら ok=false ですぐ返る。
	Poll(turn int) (c rules.Circle, ok bool, err error)
	Close() error
}

// Rematcher は「同じ相手ともう一度」ができる相手役。
//
// **これを満たすかどうかが、結果画面に再戦を出すかどうかを決める。**
// オンラインの相手役（internal/netplay の NetOpponent）は満たし、CPU の相手役は
// 満たさない。だから「オンラインなら出す」という判定をどこにも書かなくてよい。
//
// Opponent と同じく、どのメソッドもブロックしてはならない。結果画面も
// 毎フレーム呼ばれる。
type Rematcher interface {
	// RequestRematch は再戦の申し出を相手に伝える。turn は終わった対戦の最終ターン。
	RequestRematch(turn int) error
	// PollRematch は再戦が成立していれば true を返す。まだなら false ですぐ返る。
	PollRematch() (bool, error)
	// RematchRemaining は相手の返事を待つ残り時間。待っていなければ ok=false。
	RematchRemaining() (time.Duration, bool)
}

// cpuOpponent は CPU 対戦の相手。Submit で手を計算し、Poll は即座に返す。
type cpuOpponent struct {
	chooser ai.Chooser
	rnd     *rand.Rand
	hist    []rules.Move
	cfg     rules.Config
	seat    rules.Player
	pending map[int]rules.Circle
}

// NewCPUOpponent は CPU の相手を作る。
func NewCPUOpponent(d ai.Difficulty, seed int64) Opponent {
	return newCPUOpponentWith(ai.New(d), seed)
}

// newCPUOpponentWith は Chooser を差し替えられる版。テスト専用。
func newCPUOpponentWith(ch ai.Chooser, seed int64) Opponent {
	return &cpuOpponent{
		chooser: ch,
		rnd:     rand.New(rand.NewSource(seed)),
		seat:    rules.P2,
		pending: map[int]rules.Circle{},
	}
}

// SetContext は CPU が参照してよい情報を渡す。
// 渡すのは公開済みの履歴と自分の席だけで、プレイヤーの未確定の円は渡さない。
//
// 席を引数で受け取るのは、CPU 側が rules.P2 を決め打ちしないためである。
// CPU 対戦では席は必ず「プレイヤー=P1・CPU=P2」だが、その前提を
// cpuOpponent の中に埋め込むと、PlayState 側の席と食い違ったときに
// 黙って間違う。席の食い違いはオンライン対戦で実際に起きた欠陥
// （両者が自分を P1 として盤面を作り board-mismatch になる）と同じ形なので、
// 決め打ちを残さず PlayState から引くようにしてある。
func (o *cpuOpponent) SetContext(cfg rules.Config, hist []rules.Move, seat rules.Player) {
	o.cfg = cfg
	o.hist = hist
	o.seat = seat
}

// Submit は CPU の手を決める。引数 mine（プレイヤーの確定済みの円）は
// **意図的に読まない**。読めば同時進行ではなく後出しになってしまう。
func (o *cpuOpponent) Submit(_ rules.Circle, turn int) error {
	if _, ok := o.pending[turn]; ok {
		return nil
	}
	o.pending[turn] = o.chooser.Choose(o.hist, o.cfg, o.seat, turn, o.rnd)
	return nil
}

func (o *cpuOpponent) Poll(turn int) (rules.Circle, bool, error) {
	c, ok := o.pending[turn]
	return c, ok, nil
}

func (o *cpuOpponent) Close() error { return nil }
