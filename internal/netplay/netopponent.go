package netplay

import (
	"errors"
	"fmt"
	"time"

	"github.com/morststs/inkmaru-web/internal/rules"
	"github.com/morststs/inkmaru-web/internal/ui"
)

// コンパイル時の表明。PlayState は Opponent インタフェースで相手を持ち、
// 盤面ハッシュの照合と制限時間は型アサーションで拾う（internal/ui/playstate.go）。
// どれかのシグネチャがずれても、ここで気付く。型アサーションは外れても
// コンパイルが通ってしまい、機能が黙って消えるだけなので、特に重要である。
var (
	_ ui.Opponent = (*NetOpponent)(nil)
	_ interface {
		ConfirmBoard(*rules.Board, int) (bool, error)
	} = (*NetOpponent)(nil)
	_ interface {
		Deadline(turn int) (time.Time, bool)
		Now() time.Time
	} = (*NetOpponent)(nil)
	// 再戦は ui.Rematcher を満たすかどうかで画面に出る。外れると
	// **ボタンが黙って消える**（コンパイルは通る）ので、ここで固定する。
	_ ui.Rematcher = (*NetOpponent)(nil)
)

// RematchLimit は「もう一度」を申し出てから相手の返事を待つ時間。
//
// 1ターンの TurnLimit(30秒) より長いのは、性質が違うためである。ターンの30秒は
// 「描いて決める」時間だが、ここは結果を読んでから相手の意思を待つ時間で、
// 相手も同じ画面を読んでいる。
const RematchLimit = 60 * time.Second

// NetOpponent は Session を ui.Opponent の形に合わせる。
//
// 依存の向きは netplay → ui の一方向である。internal/ui は internal/netplay を
// import しない（そうすると ui のヘッドレスなテスト一式が通信の都合を背負う）ので
// 循環はしない。ここで ui を import しているのは切断の番兵 ui.ErrPeerGone と
// ui.PeerFault のためだけであり、ui.Opponent インタフェース自体は構造的に満たしている。
//
// **並行呼び出しは安全でない。** map をそのまま持っており、ゲームループ1本から
// 呼ばれる前提である。WebRTC の ondatachannel コールバックは別 goroutine で
// 走りうるが、その緩衝は Transport 実装側の責任である（transport.go の契約）。
type NetOpponent struct {
	s   *Session
	now func() time.Time

	// 相手待ちの打ち切り。相手が繋がったまま黙り込むと、こちらは永久に
	// 待たされる（悪意がなくても半開きの接続で起きる）。待ちにも同じ
	// TurnLimit を課し、超えたら切断として扱う。
	waitKind waitKind
	waitTurn int
	waitAt   time.Time
	expired  error // 打ち切ったあとは同じエラーを返し続ける

	// start はターンごとの描画フェーズの起点。ターン0は Start()（呼ばれ
	// なければ最初に Deadline を参照した時点）、ターン t+1 は
	// ConfirmBoard(_, t) が一致を返した瞬間に立つ。
	//
	// Submit や Poll では立てない。それらが最初に呼ばれるのは
	// 「プレイヤーが確定したあと」なので（PlayState は描画フェーズの間
	// Opponent に触らない）、起点にするとプレイヤーが考えていた時間が
	// まるごと制限時間の外に出て、30秒が空文になる。
	start map[int]time.Time

	// rematchAt は再戦を申し出た時刻。ゼロ値なら申し出ていない。
	// 相手の返事を待つ時計であり、ターンの待ち（waitKind）とは別に持つ。
	// 同じ時計に相乗りさせると、対戦中の30秒と結果画面の60秒が混ざる。
	rematchAt time.Time
}

// NewNetOpponent はオンライン対戦の相手を作る。
//
// **制限時間はここでは始まらない。Start() を呼ぶこと。** NewNetOpponent は
// DataChannel が確立した時点で作られるが、そこから対戦画面に入るまでには
// ICE の完了待ちや「相手が見つかりました」の表示が挟まりうる。構築時に
// 数え始めると、その数秒がプレイヤーの30秒から引かれる。
func NewNetOpponent(t Transport, now func() time.Time) *NetOpponent {
	return &NetOpponent{
		s:     NewSession(t, now),
		now:   now,
		start: map[int]time.Time{},
	}
}

// Start は対戦の開始を宣言し、ターン0の制限時間を数え始める。
//
// 対戦画面に入る直前（ui.NewPlayStateWith を呼ぶ直前）に1回だけ呼ぶこと。
// 2回目以降は無視する。これが最も正確な起点である。
//
// ただし**制限時間の正しさをこの呼び出しに依存させてはいない**。呼び忘れても
// Deadline が最初に参照された時点で数え始める（Deadline のコメント参照）。
// 呼び忘れが「タイマーが黙って消える」に化けるのを防ぐためである。
func (o *NetOpponent) Start() { o.markStart(0) }

// Now は相手側が使っている時計を返す。PlayState はこれと Deadline だけで
// 残り時間を判断する。PlayState 側が Tick の dt を積算して別に数えると
// 時計が2本になり、タブが背面に回ったときに必ずずれる。
func (o *NetOpponent) Now() time.Time { return o.now() }

// Deadline はいま数えている期限を返す。起点がまだ立っていなければ ok=false。
//
// ゼロ値の time.Time を返してはならない。西暦1年は「すでに時間切れ」と
// 読めてしまい、呼び出し側は対戦開始の初回フレームで自動確定してしまう。
//
// 相手待ちの間は待ちの期限を返す（**引数のターン番号は見ない**）。待ちの
// 打ち切りと画面の残り時間表示が同じ時計を見るようにするためである。これが
// 無いと、相手を待っている間じゅう残り時間が 0 に張り付き、プレイヤーには
// 固まったのか待っているのか区別がつかない。盤面ハッシュ待ちの最中は
// match.Turn() が次のターンを指しているので、引数を信じると起点の無いターンの
// 期限を答えることになる。
//
// ターン0の起点は Start() が無ければここで立てる。制限時間という中核の
// ルールの正しさを、誰かが Start() を呼んだかどうかに依存させないためである。
// ターン1以降は ConfirmBoard の一致で必ず立つので、ここでは立てない
// （リビール演出中や盤面ハッシュ待ちの最中に参照されると、次のターンの
// 30秒を前倒しで削ってしまう）。
func (o *NetOpponent) Deadline(turn int) (time.Time, bool) {
	if o.waitKind != waitNone {
		return o.waitAt.Add(TurnLimit), true
	}
	t, ok := o.start[turn]
	if !ok {
		if turn != 0 {
			return time.Time{}, false
		}
		o.markStart(0)
		t = o.start[0]
	}
	return t.Add(TurnLimit), true
}

func (o *NetOpponent) Submit(mine rules.Circle, turn int) error {
	if o.expired != nil {
		return o.expired
	}
	return o.translate(o.s.Submit(mine, turn))
}

// Poll は相手の手が確定していれば返す。まだなら ok=false ですぐ返る。
//
// **ここで自分の時間切れを提出してはならない。** wire に出る円を決める経路が
// PlayState.Confirm とここの2本になると、時間切れの自動提出が commit を
// 先に埋めてしまい、あとからプレイヤーが確定した円が Session.sentCommit で
// 黙って捨てられる。自分の盤面には後者、相手に届くのは前者が入って
// board-mismatch になり、防ごうとした時間切れより悪い結末（両者とも中断）になる。
// 制限時間の強制は PlayState が Deadline と Now を見て行う。
func (o *NetOpponent) Poll(turn int) (rules.Circle, bool, error) {
	if o.expired != nil {
		return rules.Circle{}, false, o.expired
	}
	c, ok, err := o.s.Poll(turn)
	if err != nil {
		return rules.Circle{}, false, o.translate(err)
	}
	if ok {
		o.endWait()
		return c, true, nil
	}
	if err := o.checkWait(waitPeerMove, turn); err != nil {
		return rules.Circle{}, false, err
	}
	return rules.Circle{}, false, nil
}

// ConfirmBoard は解決後の盤面が両者で一致するかを確かめる。
//
// PlayState はハッシュが揃うまで毎フレーム、同じ盤面でこれを呼ぶ。
// Session.ExchangeBoard は送信もハッシュ計算も1回に抑え、届いていなければ
// 即座に (false, nil) を返すので、ブロックせず何度呼んでも結果は変わらない。
// 渡された盤面は読むだけで、変更しない。
//
// 一致した瞬間が次のターンの制限時間の起点である。PlayState はここで
// リビール演出を抜けて次の描画フェーズに入る（同じフレームで）。
// 待たされた時間は次の30秒に入らないので、通信が遅い側が損をしない。
func (o *NetOpponent) ConfirmBoard(b *rules.Board, turn int) (bool, error) {
	if o.expired != nil {
		return false, o.expired
	}
	agreed, err := o.s.ExchangeBoard(b, turn)
	if err != nil {
		return false, o.translate(err)
	}
	if agreed {
		o.endWait()
		o.markStart(turn + 1)
		return true, nil
	}
	if err := o.checkWait(waitPeerBoard, turn); err != nil {
		return false, err
	}
	return false, nil
}

// waitKind は「いま何を待っているか」。
type waitKind int

const (
	waitNone      waitKind = iota
	waitPeerMove           // 相手の手（commit も reveal もここに含む）
	waitPeerBoard          // 相手の盤面ハッシュ
)

// code は打ち切りの理由に載せる識別子。AbortError の理由と同じ体裁にしてある。
func (k waitKind) code() string {
	if k == waitPeerBoard {
		return "peer-board-timeout"
	}
	return "peer-move-timeout"
}

// checkWait は待ちの時計を進め、制限時間を超えていたら打ち切る。
//
// 待ちごとに数え直す。1ターンぶんの合計にすると、相手が29秒で応答したときに
// 次の待ちが1秒しか残らない。どの待ちも「相手が黙っている時間」なので、
// それぞれに TurnLimit を与えるのが素直である。
//
// 打ち切りは切断として扱う（Ruling B により自分の勝ちになる）。「時間切れだから
// 相手の負け」という判定を相手に送りつけるわけではない。自分の観測に基づいて
// 自分の対戦を終えるだけなので、相手の時計とずれていても矛盾しない。
func (o *NetOpponent) checkWait(k waitKind, turn int) error {
	if o.waitKind != k || o.waitTurn != turn {
		o.waitKind, o.waitTurn, o.waitAt = k, turn, o.now()
	}
	if !o.now().After(o.waitAt.Add(TurnLimit)) {
		return nil
	}
	o.expired = peerFault{
		reason: "netplay: 相手の応答が途絶えた (" + k.code() + ")",
		cause:  fmt.Errorf("%w: %v を超えて応答が無い", ErrPeerGone, TurnLimit),
	}
	return o.expired
}

func (o *NetOpponent) endWait() { o.waitKind = waitNone }

func (o *NetOpponent) markStart(turn int) {
	if _, ok := o.start[turn]; !ok {
		o.start[turn] = o.now()
	}
}

// RequestRematch は「同じ相手ともう一度」を相手に伝える。ブロックしない。
//
// turn は終わった対戦の最終ターン。相手は検査しない（設計 §2）。
func (o *NetOpponent) RequestRematch(turn int) error {
	if o.expired != nil {
		return o.expired
	}
	if err := o.s.RequestRematch(turn); err != nil {
		return o.translate(err)
	}
	if o.rematchAt.IsZero() {
		o.rematchAt = o.now()
	}
	return nil
}

// PollRematch は再戦が成立したかを返す。まだなら false ですぐ返る。
//
// 成立した時点で制限時間の起点を捨てる。**捨て忘れると対戦2のターン0の期限が
// 対戦1の開始時刻＋TurnLimit のままになり、始まった瞬間に時間切れになる**
// （設計 §4）。対戦1が終わるまで何も起きないので、実際に再戦するまで気づけない。
//
// 申し出ていなければ何もしない。相手だけが申し出ている状態で待ち続けるのは、
// こちらが結果画面を読んでいる時間であって、打ち切る理由が無い。
func (o *NetOpponent) PollRematch() (bool, error) {
	if o.expired != nil {
		return false, o.expired
	}
	if o.rematchAt.IsZero() {
		return false, nil
	}
	agreed, err := o.s.RematchAgreed()
	if err != nil {
		return false, o.translate(err)
	}
	if agreed {
		o.start = map[int]time.Time{}
		o.endWait()
		o.rematchAt = time.Time{}
		// ここで markStart(0) は呼ばない。起点が無ければ Deadline が
		// 最初に参照された時点で立てる（Deadline のコメント参照）。二重に
		// 立てると、どちらが壊れてももう片方が同じ結果を返してしまい、
		// 退行をテストで捕まえられなくなる。
		return true, nil
	}
	if !o.now().After(o.rematchAt.Add(RematchLimit)) {
		return false, nil
	}
	o.expired = peerFault{
		reason: "netplay: 相手が再戦に応じなかった (rematch-timeout)",
		cause:  fmt.Errorf("%w: %v を超えて返事が無い", ErrPeerGone, RematchLimit),
	}
	return false, o.expired
}

// RematchRemaining は相手の返事を待つ残り時間。申し出ていなければ ok=false。
func (o *NetOpponent) RematchRemaining() (time.Duration, bool) {
	if o.rematchAt.IsZero() {
		return 0, false
	}
	d := o.rematchAt.Add(RematchLimit).Sub(o.now())
	if d < 0 {
		d = 0
	}
	return d, true
}

func (o *NetOpponent) Close() error { return o.s.Close() }

// peerFault は相手側の落ち度で対戦が終わったことを表す。ui.PeerFault を満たす。
//
// Unwrap が []error を返すので、ui.ErrPeerGone と元のエラーの両方で
// errors.Is が真になる。注意: 複数エラーを返す Unwrap を持つため
// errors.Unwrap（単数）は nil を返す。剥がすときは errors.Is / errors.As を使うこと。
type peerFault struct {
	reason string // 空なら理由の無い素の切断
	cause  error
}

func (e peerFault) Error() string {
	if e.reason == "" {
		return ui.ErrPeerGone.Error() + ": " + e.cause.Error()
	}
	return ui.ErrPeerGone.Error() + ": " + e.reason
}

func (e peerFault) Unwrap() []error         { return []error{ui.ErrPeerGone, e.cause} }
func (e peerFault) PeerFaultReason() string { return e.reason }

// translate は相手側の落ち度による終了を ui の番兵に翻訳する。
// Submit・Poll・ConfirmBoard の3経路すべてを通すこと。
//
// PlayState は errors.Is(err, ui.ErrPeerGone) で「相手の負け」を判定する
// （仕様書 §5.4・§7.2）。netplay の番兵をそのまま返すと別の値なので比較が
// false になり、勝敗のつかない中断に落ちる。それは逃げ得の動機を戻す。
//
// 中断（AbortError）も同じく相手の負けとして扱う。相手が通告してきた中断も、
// 自分が検出した不正も、原因は相手側にあるからである。負けている側が
// abort を1通投げてから切断すれば確定した敗北を引き分けに変えられる、
// という穴を塞ぐ。理由は PeerFaultReason で別に運ぶので、結果画面は
// 「あなたの勝ち」と「なぜ終わったか」を同時に出せる。
//
// 翻訳しないのは自分側の失敗（送信の変換に失敗した等）だけである。
// そこまで勝ちにすると、自分のバグで勝ってしまう。
func (o *NetOpponent) translate(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrPeerGone) {
		return peerFault{cause: err}
	}
	var ab AbortError
	if errors.As(err, &ab) {
		return peerFault{reason: ab.Error(), cause: err}
	}
	return err
}
