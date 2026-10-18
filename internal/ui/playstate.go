package ui

import (
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/morststs/inkmaru-web/internal/ai"
	"github.com/morststs/inkmaru-web/internal/rules"
)

// RevealSeconds はリビール演出にかける秒数。
const RevealSeconds = 0.6

type Phase int

const (
	PhaseDrawing Phase = iota
	// PhaseWaitingPeer は自分の手を確定し、相手の手が届くのを待っている状態。
	// CPU 対戦では相手が即答するため、この状態が外から観測されることはない。
	PhaseWaitingPeer
	PhaseRevealing
	PhaseFinished
)

var (
	ErrNoPreview  = errors.New("ui: 円が描かれていない")
	ErrNotDrawing = errors.New("ui: いま確定できるフェーズではない")
)

// PlayState は対戦シーンの進行を保持する純粋な状態機械。
// 描画エンジンに依存しないため、ヘッドレスでテストできる。
//
// 相手は Opponent 越しにしか触らない。CPU でもオンラインでも同じ進行になる。
type PlayState struct {
	match *rules.Match
	opp   Opponent

	// seat は自分がどちらのプレイヤーか。オンライン対戦では両者が同じ盤面を
	// 作れるよう役割で決める（NewPlayStateWithSeat のコメント参照）。
	// CPU 対戦では常に P1 で、第1段階の挙動と完全に同じである。
	seat rules.Player

	phase   Phase
	preview *rules.Circle

	// previewScore は preview を適用した結果の Score をキャッシュしたもの。
	// PreviewScore() は毎フレーム呼ばれる（ドラッグ中は特に）ため、その都度
	// 256×256セルの盤面を複製・再走査するのは重い。SetPreview で作り直し、
	// preview が無効になる場所（Confirm）では一緒に破棄する。
	previewScore *rules.Score

	before    *rules.Board // 演出中に描く「確定前の盤面」
	lastMoves [2]rules.Circle
	revealT   float64

	myCircle    rules.Circle // Confirm で確定し、相手の手が届くまで保持する
	abortReason string
	// rematchAsked は自分が再戦を申し出たか。
	rematchAsked bool
	// rematchFailed は相手の返事が得られなかったか（断られた・消えた・時間切れ）。
	// **勝敗には触らない。** 対戦そのものは正常に終わっており、後から
	// 変わってよい結果ではない。
	rematchFailed bool // 中断の理由。空でなければ画面が表示する
	peerLeft      bool // 相手が切断した。自分の勝ちとして終える

	// boardSnap は盤面ハッシュの照合に渡す複製。相手のハッシュが届くまで
	// 毎フレーム ConfirmBoard を呼ぶが、その間 match の盤面は変化しない
	// （次のターンはまだ始まっていない）ので、複製は1回だけ作って使い回す。
	// リビール演出を抜けるときに捨て、次のターンでは作り直す。
	boardSnap *rules.Board
}

// NewPlayState は CPU 対戦の状態を作る。
// 第1段階からのシグネチャを保つため、内部で CPU の Opponent を組み立てる。
func NewPlayState(cfg rules.Config, d ai.Difficulty, seed int64) *PlayState {
	return NewPlayStateWith(cfg, NewCPUOpponent(d, seed))
}

// NewPlayStateWith は相手を差し替えられる版。席は P1 になる。
func NewPlayStateWith(cfg rules.Config, opp Opponent) *PlayState {
	return NewPlayStateWithSeat(cfg, opp, rules.P1)
}

// NewPlayStateWithSeat は自分の席を指定する版。オンライン対戦はこちらを使う。
//
// **席は役割で決まる。部屋を作ったホストが P1、参加したゲストが P2 である。**
// 両者が自分を P1 として盤面に書き込むと、盤面は互いの鏡像になる
// （私が P1 の陣地として記録したセルを、相手は P2 の陣地として記録する）。
// netplay.BoardHash はセルごとに byte(OwnerAt) を並べるので、鏡像の盤面は
// 必ず違うハッシュになり、1ターン目の照合が board-mismatch で落ちる。
// 席を役割で決めれば両者はバイト単位で同一の盤面を作る。
//
// 副次的な利点として、色も両画面で一致する。「自分がいつも青」にすると、
// 同じ盤面が2つの画面で違って見えることになる。
//
// P1・P2 以外を渡した場合は P1 として扱う。
func NewPlayStateWithSeat(cfg rules.Config, opp Opponent, seat rules.Player) *PlayState {
	if seat != rules.P2 {
		seat = rules.P1
	}
	return &PlayState{
		match: rules.NewMatch(cfg),
		opp:   opp,
		phase: PhaseDrawing,
		seat:  seat,
	}
}

// Seat は自分がどちらの席かを返す。表示層は結果の文言や自分の色を
// これで決める（「あなた」がどちらの得点かは席で決まる）。
func (s *PlayState) Seat() rules.Player { return s.seat }

// peerSeat は相手の席。
func (s *PlayState) peerSeat() rules.Player { return PeerOf(s.seat) }

func (s *PlayState) Phase() Phase               { return s.phase }
func (s *PlayState) Match() *rules.Match        { return s.match }
func (s *PlayState) BoardBefore() *rules.Board  { return s.before }
func (s *PlayState) LastMoves() [2]rules.Circle { return s.lastMoves }
func (s *PlayState) RevealProgress() float64    { return s.revealT }

// AbortReason は対戦が終わった理由。空なら理由の無い普通の終わり方である。
//
// PeerLeft() と直交する。素の切断では PeerLeft() だけが立って理由は空になり、
// 相手が原因の中断では PeerLeft() が立ったうえで理由も入る（「あなたの勝ち」と
// 「相手のデータが不正でした」を同時に出せる）。PeerLeft() が偽のまま理由だけが
// 入っているのは、勝敗のつかない自分側の失敗である。
func (s *PlayState) AbortReason() string { return s.abortReason }

// PeerLeft は相手が対戦から離れたか。真なら自分の勝ちである（仕様書 §5.4）。
func (s *PlayState) PeerLeft() bool { return s.peerLeft }

// Close は相手側の資源を解放する。オンラインでは接続を閉じる。
//
// **再戦するときは呼んではならない。** 呼ぶと DataChannel ごと閉じるので、
// 次の対戦の相手が居なくなる（再戦の設計 §8）。
func (s *PlayState) Close() error { return s.opp.Close() }

// rematcher は相手役が再戦に対応していれば返す。
func (s *PlayState) rematcher() (Rematcher, bool) {
	r, ok := s.opp.(Rematcher)
	return r, ok
}

// CanRematch は結果画面に「もう一度」を出してよいかを返す。
//
// 出すのは、相手役が再戦に対応していて、かつ**正常に終わった**対戦だけである。
// 中断で終わった対戦に出さないのは、不正を検出した相手と再戦する理由が無いため。
// 相手が離脱した対戦に出さないのは、相手がもう居ないためである（設計 §6）。
func (s *PlayState) CanRematch() bool {
	if s.phase != PhaseFinished || s.abortReason != "" || s.peerLeft || s.rematchFailed {
		return false
	}
	_, ok := s.rematcher()
	return ok
}

// RematchAsked は自分が既に申し出たか。画面はこれでボタンと待ち表示を切り替える。
func (s *PlayState) RematchAsked() bool { return s.rematchAsked }

// RequestRematch は「もう一度」を相手に伝える。2回目以降は何もしない。
func (s *PlayState) RequestRematch() error {
	if !s.CanRematch() {
		return errors.New("ui: この対戦では再戦できない")
	}
	if s.rematchAsked {
		return nil
	}
	r, _ := s.rematcher()
	if err := r.RequestRematch(s.match.Turn() - 1); err != nil {
		return err
	}
	s.rematchAsked = true
	return nil
}

// PollRematch は再戦が成立したかを返す。申し出るまでは何もしない。
//
// 申し出る前から相手の返事を見に行くと、こちらが結果を読んでいるあいだに
// 相手の待ち時間が進む。
func (s *PlayState) PollRematch() (bool, error) {
	if !s.rematchAsked {
		return false, nil
	}
	r, ok := s.rematcher()
	if !ok {
		return false, nil
	}
	agreed, err := r.PollRematch()
	if err != nil {
		// **勝敗は上書きしない。** 相手が消えたのは対戦が終わったあとなので、
		// 対戦中の離脱（PeerLeft＝自分の勝ち）として扱うと、負けた対戦が
		// 勝ちに見える。
		s.rematchFailed = true
	}
	return agreed, err
}

// RematchStatus は再戦の申し出について画面に出す行を返す。空なら何も出さない。
//
// 秒の丸めをここに置くのは、これが表示の判断だからである。gfx に置くと
// 「のこり 0秒」と出たまま待ち続けるような食い違いを誰も検査できない。
func (s *PlayState) RematchStatus() []string {
	if s.rematchFailed {
		return []string{"相手は戻りませんでした"}
	}
	d, ok := s.RematchRemaining()
	if !ok {
		return nil
	}
	return []string{
		"相手の返事を待っています",
		fmt.Sprintf("のこり %d秒", int(d.Seconds()+0.5)),
	}
}

// RematchRemaining は相手の返事を待つ残り時間。待っていなければ ok=false。
func (s *PlayState) RematchRemaining() (time.Duration, bool) {
	if !s.rematchAsked {
		return 0, false
	}
	r, ok := s.rematcher()
	if !ok {
		return 0, false
	}
	return r.RematchRemaining()
}

func (s *PlayState) SetPreview(c rules.Circle) {
	// 描画フェーズでのみプレビューを受け付ける。
	// 演出中や相手待ちの入力は無視する（呼び出し側が正しくゲートしていない場合の防御）。
	if s.phase != PhaseDrawing {
		return
	}
	cc := s.match.Config().ClampCircle(c)
	s.preview = &cc

	tmp := s.match.Board().Clone()
	tmp.Apply(cc, s.seat, s.match.Config().InkForTurn(s.match.Turn()))
	sc := tmp.Score()
	s.previewScore = &sc
}

func (s *PlayState) Preview() (rules.Circle, bool) {
	if s.preview == nil {
		return rules.Circle{}, false
	}
	return *s.preview, true
}

// PreviewScore は「いま確定したら陣地がどうなるか」を返す。
// 相手の未確定の円は当然含まれない。
// 値は SetPreview の時点で計算済みのキャッシュを返すだけであり、
// ここで盤面を複製・再走査することはない（ドラッグ中は毎フレーム呼ばれるため）。
func (s *PlayState) PreviewScore() rules.Score {
	if s.preview == nil || s.previewScore == nil {
		return s.match.Board().Score()
	}
	return *s.previewScore
}

// Confirm はプレイヤーの円を確定し、相手に伝えて相手待ちに入る。
//
// ターンの解決は相手の手が揃ってからでないとできない。相手が即答するなら
// （CPU がそうである）その場で解決してリビール演出まで進むので、第1段階の
// 観測できる挙動は変わらない。即答しない相手なら PhaseWaitingPeer に留まり、
// 以降は Tick が毎フレーム様子を見る。どちらの経路でもここではブロックしない。
func (s *PlayState) Confirm() error {
	if s.phase != PhaseDrawing {
		return ErrNotDrawing
	}
	if s.preview == nil {
		return ErrNoPreview
	}
	turn := s.match.Turn()
	// CPU に渡すのは公開済みの履歴だけ。プレイヤーの円は渡さない。
	// 防御多層の原則: SetContext は Submit 前に呼ぶ（ここでの順序）。
	// しかし真の保証は internal/rules.Match.History() が解決済みの手だけを
	// 返すこと（TestHistoryExcludesUnresolvedMoves で検証）であり、
	// ここでの順序を変えても History() は未確定の手を含まない。
	if sc, ok := s.opp.(interface {
		SetContext(rules.Config, []rules.Move, rules.Player)
	}); ok {
		sc.SetContext(s.match.Config(), s.match.History(), s.peerSeat())
	}
	s.myCircle = *s.preview
	if err := s.opp.Submit(s.myCircle, turn); err != nil {
		// 送信の失敗も対戦の終わりである。エラーを呼び出し側に返すだけでは
		// 済まない。gfx は Confirm の戻り値を捨てており（scene_play.go）、
		// 何より「相手が消えた」は勝敗のつく正常な終了だからである。
		// Poll と同じ分類器に通し、フェーズを必ず前に進める。
		//
		// これはオンラインで最も起きやすい切断のかたちを拾うためでもある。
		// 自分が描いている間に相手がタブを閉じると、送信の時点で初めて分かる。
		s.finishOnOpponentError(err)
		return nil
	}
	s.before = s.match.Board().Clone()
	s.preview = nil
	s.previewScore = nil
	s.phase = PhaseWaitingPeer
	s.resolveIfPeerReady()
	return nil
}

// finishOnOpponentError は Opponent から返ったエラーで対戦を終える。
// Submit と Poll の両方から呼ぶ。分類が2箇所に分かれていると、
// 片方だけ直して片方を忘れる（実際にそうなった）。
func (s *PlayState) finishOnOpponentError(err error) {
	// 相手が消えた場合は自分の勝ち。逃げ得を防ぐため（仕様書 §5.4）。
	//
	// 相手が原因の中断も同じ扱いである。負けている側が
	// abort{reason:"hash-mismatch"} を1通投げてから切断すれば、確定していた
	// 敗北を「勝敗なしの中断」に変えられてしまう。それは §5.4 が消したはずの
	// 逃げ得が別の入口から戻ってくることに他ならない。審判がいない以上、
	// 自分の観測しか信じられないので、「あなたが不正をした」という相手の主張は
	// 嘘か相手のバグかのどちらかであり、どちらであっても自分が得た結果を
	// 手放す理由にならない。自分で不正を検出した場合も同様に、不正をした側が
	// 引き分けを得てはならない。
	//
	// 代償として、双方が正直でも片方にバグがあると両方の画面が「あなたの勝ち」に
	// なる。審判がいない以上、全体で整合する答えは存在しない。2つの「勝ち」は
	// 2つの「引き分け」より動機の面で厳密に良い。
	//
	// 勝敗をつけないまま終わるのは、自分側の失敗（送信の変換に失敗した等）だけである。
	if errors.Is(err, ErrPeerGone) {
		s.peerLeft = true
		// 誰が勝ったかとは別に、なぜ終わったかも結果画面に出す。
		var pf PeerFault
		if errors.As(err, &pf) {
			s.abortReason = pf.PeerFaultReason()
		}
	} else {
		s.abortReason = err.Error()
	}
	s.preview = nil
	s.previewScore = nil
	s.boardSnap = nil
	s.phase = PhaseFinished
}

// abortWith は勝敗をつけずに理由を残して終える。
// 相手起因のエラーはここではなく finishOnOpponentError を通すこと。
// 切断は勝敗のつく正常な終了であり、中断とは別物である。
func (s *PlayState) abortWith(err error) {
	s.abortReason = err.Error()
	s.boardSnap = nil
	s.phase = PhaseFinished
}

// resolveIfPeerReady は相手の手が届いていればターンを解決する。
// 届いていなければ何もせず戻る。ゲームループを止めないことが要件なので、
// ここでは決して待たない。
func (s *PlayState) resolveIfPeerReady() {
	turn := s.match.Turn()
	peer, ok, err := s.opp.Poll(turn)
	if err != nil {
		s.finishOnOpponentError(err)
		return
	}
	if !ok {
		return
	}
	// ここから先の失敗は自分側の不整合であり、黙って return すると
	// 次のフレームも同じところで失敗して待ちフェーズに永久に固まる。
	// 現状の rules.Match の不変条件では到達しないはずだが、到達したときに
	// 画面が無言で止まるより、中断として理由を見せるほうが安全である。
	// 自分は自分の席で、相手はもう一方の席で提出する。両者が自分を P1 に
	// すると盤面が鏡像になり、盤面ハッシュの照合が必ず食い違う。
	if err := s.match.Submit(s.seat, s.myCircle); err != nil {
		s.abortWith(err)
		return
	}
	if err := s.match.Submit(s.peerSeat(), peer); err != nil {
		s.abortWith(err)
		return
	}
	moves, resolved := s.match.Resolve()
	if !resolved {
		s.abortWith(errors.New("ui: ターンを解決できなかった"))
		return
	}
	s.lastMoves = moves
	s.revealT = 0
	s.phase = PhaseRevealing
}

// deadliner は制限時間を持つ相手。オンライン対戦の相手だけが実装する。
//
// Opponent 本体には入れない。CPU 対戦に制限時間は無いので、cpuOpponent が
// 実装しないことで第1段階の挙動がそのまま残る（SetContext・ConfirmBoard と
// 同じ任意インタフェースの型アサーション）。
//
// 時計を相手からもらうのが要点である。PlayState が Tick の dt を積算して
// 自分で数えると時計が2本になる。ブラウザのタブが背面に回ると
// requestAnimationFrame が止まって dt の積算は凍るが壁時計は進むので、
// 2本の時計は必ずずれる。Deadline も Session の判断も同じ now から来る形なら
// 時計は1本のままである。
type deadliner interface {
	// Deadline はそのターンの期限。起点がまだ立っていなければ ok=false を返す。
	// ゼロ値の time.Time を返させてはならない。西暦1年は「すでに時間切れ」と
	// 読めてしまい、対戦開始の初回フレームで自動確定する。
	Deadline(turn int) (time.Time, bool)
	Now() time.Time
}

// Remaining はいまのターンの残り時間。制限時間の無い相手（CPU）や、
// まだ起点が立っていないターンでは ok=false を返す。画面の表示に使う。
func (s *PlayState) Remaining() (time.Duration, bool) {
	d, ok := s.opp.(deadliner)
	if !ok {
		return 0, false
	}
	dl, anchored := d.Deadline(s.match.Turn())
	if !anchored {
		return 0, false
	}
	left := dl.Sub(d.Now())
	if left < 0 {
		left = 0
	}
	return left, true
}

// enforceDeadline は制限時間を過ぎていたらプレイヤーの代わりに確定する（仕様書 §7.1）。
//
// 提出は必ず Confirm を通す。wire に出る円を決める経路が2本あると
// 「自分の盤面に入る円 ≠ 相手に送った円」が起きて board-mismatch になり、
// 防ごうとした時間切れより悪い結末（両者とも勝敗なしの中断）を作る。
//
// 「時間切れだから相手の負け」という判定は相手に送らない。自分の時計で
// 自分の手を決めるだけなので、両者の時計がずれていても矛盾しない。
// 相手にはただの手が1つ届くだけである。
func (s *PlayState) enforceDeadline() {
	d, ok := s.opp.(deadliner)
	if !ok {
		return
	}
	dl, anchored := d.Deadline(s.match.Turn())
	if !anchored {
		return
	}
	now := d.Now()
	if now.Before(dl) {
		return
	}
	if s.preview == nil {
		s.SetPreview(s.timeoutCircle(now))
	}
	// 戻り値は捨てる。Confirm の中の失敗は finishOnOpponentError／abortWith が
	// フェーズを前に進めるので、ここで握るものは無い。プレビューは直前に
	// 用意してあるので ErrNoPreview にはならない。
	_ = s.Confirm()
}

// timeoutCircle は何も描かないまま時間切れになったときに出す円を返す。
// 仕様書 §7.1 のとおり最小半径。位置は時計から導く。
//
// 乱数の種を時計から取るのは、PlayState に別の乱数源を持ち込まないためであり、
// 同時に時計を注入したテストで決定的にするためでもある。位置が読まれても損は
// 無い（commit-reveal で隠れるうえ、最小円の影響は小さい）。
func (s *PlayState) timeoutCircle(now time.Time) rules.Circle {
	cfg := s.match.Config()
	rnd := rand.New(rand.NewSource(now.UnixNano()))
	return cfg.ClampCircle(rules.Circle{CX: rnd.Float64(), CY: rnd.Float64(), R: cfg.MinRadius()})
}

// Tick は演出を dt 秒ぶん進める。
func (s *PlayState) Tick(dt float64) {
	if s.phase == PhaseDrawing {
		s.enforceDeadline()
		return
	}
	if s.phase == PhaseWaitingPeer {
		s.resolveIfPeerReady()
		return
	}
	if s.phase != PhaseRevealing {
		return
	}
	s.revealT += dt / RevealSeconds
	if s.revealT < 1 {
		return
	}
	s.revealT = 1

	// 盤面ハッシュの照合が済むまで次のターンに進まない。
	// P2P には審判がいないので、両者の計算結果が食い違っても誰も裁定できない。
	// 32バイトの交換で検出できるため、通信量を理由に省く意味はない。
	// CPU 対戦の相手は ConfirmBoard を持たないので、この分岐は素通りする。
	if cb, ok := s.opp.(interface {
		ConfirmBoard(*rules.Board, int) (bool, error)
	}); ok {
		// 渡すのは複製である。相手の実装に必要なのはハッシュを取ることだけで、
		// 本物の盤面を握らせる理由が無い。不注意な実装が Apply でも呼べば
		// 対戦状態が壊れる。
		//
		// 複製は使い回す。rules.Board は maxD []float32（1セル4バイト）と
		// mask []uint8（1セル1バイト）を持つので、GridN=256 の Clone は
		// 約320KB（327,684バイト）である。相手のハッシュが届くまで
		// 毎フレーム呼ぶ経路なので、毎回複製すると WASM では効いてくる。
		// 待っている間 match の盤面は変化しないため、使い回しても結果は同じである。
		if s.boardSnap == nil {
			s.boardSnap = s.match.Board().Clone()
		}
		agreed, err := cb.ConfirmBoard(s.boardSnap, s.match.Turn()-1)
		if err != nil {
			// 相手起因のエラーなので Submit・Poll と同じ分類器に通す。
			// ここだけ中断扱いにすると、相手が消えた瞬間が1フレーム違うだけで
			// 「あなたの勝ち」と「エラーで中断」が入れ替わってしまう。
			s.finishOnOpponentError(err)
			return
		}
		if !agreed {
			return // まだ相手のハッシュが届いていない。次のフレームで再試行する
		}
	}
	s.boardSnap = nil
	if s.match.Finished() {
		s.phase = PhaseFinished
	} else {
		s.phase = PhaseDrawing
	}
}
