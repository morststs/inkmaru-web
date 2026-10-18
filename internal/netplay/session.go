package netplay

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/morststs/inkmaru-web/internal/rules"
)

// TurnLimit は1ターンの制限時間。
//
// 時間切れの判定を相手に委ねないことが要点である。P2P には審判がいないので、
// 両者が独立に数える。「時間切れだから相手の負け」という判定を押し付けると
// 時計のずれで認識が食い違うが、自分の時計で自分の手を決める形なら矛盾しない。
const TurnLimit = 30 * time.Second

// pendingCap は保留できる「1手先行した相手」からのメッセージの上限。
//
// 正しく振る舞う相手が1ターンに送るのは commit・reveal・board の高々数通で、
// 1手先行しうるのはそのうちの一部でしかない。上限を設けないと、悪意ある
// 相手が未来ターンのメッセージを際限なく送りつけて s.pending を無限に
// 伸ばせてしまう（レビュー再検証で5000件のメモリ増加を実証）。
const pendingCap = 8

// AbortError は対戦を中断すべき事象を表す。
type AbortError struct{ Reason string }

func (e AbortError) Error() string { return "netplay: 中断 (" + e.Reason + ")" }

// Session は1つの対戦の相手側とのやりとりを受け持つ。
type Session struct {
	tr  Transport
	now func() time.Time

	turn          int
	myNonce       []byte
	myCircle      rules.Circle
	sentCommit    bool
	sentReveal    bool
	peerCommit    string
	peerCircle    rules.Circle
	gotPeerReveal bool

	myBoardSent bool
	myBoardHash string // 待機中に毎フレーム 66KB を再ハッシュしないための控え
	peerBoard   string

	// pending は「1手先行した相手」から届いた、まだ自分のターンではない
	// メッセージの保留置き場（レビュー所見2）。相手は自分の board を見るまで
	// それ以上先へは進めないので、最大でも1ターン先行分しか積まれない。
	// resetTurn がターンを進めた直後に、その新しいターン宛てのものから
	// 順番に再生する。
	pending []Message

	// 再戦の申し出。**対戦をまたいで持ち越す唯一の状態**である。
	// sentRematch と gotRematch が揃った瞬間に、対戦1の状態をすべて捨てて
	// agreed を立てる（再戦の設計 §2・§4）。
	sentRematch bool
	gotRematch  bool
	agreed      bool

	aborted error
}

func NewSession(t Transport, now func() time.Time) *Session {
	return &Session{tr: t, now: now, turn: -1}
}

func (s *Session) Close() error { return s.tr.Close() }

// resetTurn は新しいターンの状態に切り替え、保留していたメッセージのうち
// このターン宛てのものを再生する。再生した結果 abort に至ることがあるため
// エラーを返す（呼び出し元の Submit / ExchangeBoard はこれを伝播すること）。
func (s *Session) resetTurn(turn int) error {
	pending := s.pending
	s.pending = nil
	s.turn = turn
	s.myNonce = nil
	s.sentCommit = false
	s.sentReveal = false
	s.peerCommit = ""
	s.gotPeerReveal = false
	s.myBoardSent = false
	s.myBoardHash = ""
	s.peerBoard = ""
	for _, m := range pending {
		if err := s.handleMsg(m); err != nil {
			return err
		}
	}
	return nil
}

// Submit は自分の手を確定し、commit を送る。ブロックしない。
//
// reveal はここでは送らない。両者の commit が揃うまで待つ必要があり、
// 先に送ると相手は自分の commit を出す前に実体を知って後出しできる。
func (s *Session) Submit(c rules.Circle, turn int) error {
	if s.aborted != nil {
		return s.aborted
	}
	if turn != s.turn {
		if err := s.resetTurn(turn); err != nil {
			return err
		}
	}
	if s.sentCommit {
		return nil
	}
	nonce, err := NewNonce()
	if err != nil {
		return err
	}
	s.myNonce = nonce
	s.myCircle = c
	if err := s.send(Message{T: MsgCommit, Turn: turn, Hash: CommitHash(c, nonce)}); err != nil {
		return err
	}
	s.sentCommit = true
	return s.maybeReveal()
}

// Poll は相手の手が確定していれば返す。まだなら ok=false ですぐ返る。
func (s *Session) Poll(turn int) (rules.Circle, bool, error) {
	if s.aborted != nil {
		return rules.Circle{}, false, s.aborted
	}
	if turn != s.turn {
		return rules.Circle{}, false, nil
	}
	if err := s.drain(); err != nil {
		return rules.Circle{}, false, err
	}
	if s.gotPeerReveal {
		return s.peerCircle, true, nil
	}
	return rules.Circle{}, false, nil
}

// ExchangeBoard は解決後の盤面ハッシュを交換する。
// 一致を確認できたら true を返す。食い違えば AbortError を返す。
func (s *Session) ExchangeBoard(b *rules.Board, turn int) (bool, error) {
	if s.aborted != nil {
		return false, s.aborted
	}
	if turn != s.turn {
		if err := s.resetTurn(turn); err != nil {
			return false, err
		}
	}
	// 盤面はこのターンの間もう変化しないので、ハッシュは1回だけ計算する。
	// 相手のハッシュが届くまで毎フレーム呼ばれる経路であり、GridN=256 では
	// 1回あたり 65,536 セルの走査と 64KB の SHA-256（実測 0.21ms・66KB）になる。
	// 予算内ではあるが、待っている間ずっと毎秒 4MB のゴミを出す理由は無い。
	if s.myBoardHash == "" {
		s.myBoardHash = BoardHash(b)
	}
	mine := s.myBoardHash
	if !s.myBoardSent {
		if err := s.send(Message{T: MsgBoard, Turn: turn, Hash: mine}); err != nil {
			return false, err
		}
		s.myBoardSent = true
	}
	if err := s.drain(); err != nil {
		return false, err
	}
	if s.peerBoard == "" {
		return false, nil
	}
	if s.peerBoard != mine {
		return false, s.abort(ReasonBoardMismatch)
	}
	return true, nil
}

// drain は届いているメッセージをすべて処理する。届いていなければ何もしない。
//
// 期限は 0 にしてある。Transport.Receive は届いている分を締切の状態によらず
// 優先して返す契約なので（transport.go の doc コメント参照）、0 でも既に
// キューにあるメッセージは変わらず届く。Poll は毎フレーム呼ばれるので、
// ここで無駄に待たないことが性能上重要である。
func (s *Session) drain() error {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 0)
		raw, err := s.tr.Receive(ctx)
		cancel()
		if err != nil {
			// 「何も届いていないだけ」の合図は、実装によって
			// context.DeadlineExceeded のことも context.Canceled のことも
			// ありうる（transport.go の doc コメント参照）。どちらも
			// 致命的エラーとして扱ってはならない。
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return nil // 届いていないだけ
			}
			s.aborted = err
			return err
		}
		if err := s.handleRaw(raw); err != nil {
			return err
		}
	}
}

func (s *Session) handleRaw(raw []byte) error {
	var m Message
	if err := json.Unmarshal(raw, &m); err != nil {
		return s.abort(ReasonProtocol)
	}
	return s.handleMsg(m)
}

func (s *Session) handleMsg(m Message) error {
	if m.T == MsgAbort {
		reason := m.Reason
		if reason != ReasonHashMismatch && reason != ReasonBoardMismatch && reason != ReasonProtocol {
			// レビュー所見6。相手が指定する理由は自由な文字列でありうる。
			// Task 4 はこれをそのまま画面に表示するので、既知の3つ以外は
			// protocol に丸めて渡す。
			reason = ReasonProtocol
		}
		s.aborted = AbortError{Reason: reason}
		return s.aborted
	}
	if m.T == MsgRematch {
		// **ターン番号を検査しない**（設計 §2）。どのターンで終わったかは
		// 相手の自己申告であり、こちらの s.turn と一致する保証も無い
		// （盤面ハッシュの交換まで終えた側と終えていない側で1つずれる）。
		// 検査しても防げる不正は無く、「正しい相手なのに再戦できない」失敗だけが増える。
		if s.gotRematch {
			return s.abort(ReasonProtocol) // 1回の対戦につき1通だけ
		}
		s.gotRematch = true
		s.maybeAgreeRematch()
		return nil
	}
	if m.Turn != s.turn {
		if m.Turn == s.turn+1 {
			// レビュー所見2。相手は自分の board を見るまで次のターンへ
			// 進めないので、最大でも1ターンしか先行しえない。これは
			// 正しく振る舞う相手の正常な挙動であり、捨てたり中断したり
			// してはならない。自分がそのターンへ進むまで保留する。
			if len(s.pending) >= pendingCap {
				// Fix round 2 所見1。上限を超える保留は悪意ある相手による
				// メモリ消費攻撃としてしか説明がつかない。
				return s.abort(ReasonProtocol)
			}
			s.pending = append(s.pending, m)
			return nil
		}
		return s.abort(ReasonProtocol)
	}
	switch m.T {
	case MsgCommit:
		if s.peerCommit != "" {
			// レビュー所見1（致命的）。1ターンにつき commit は1通だけ。
			// 2通目を許すと、相手は1通目で被害者に reveal させておいて
			// 円を読んでから2通目の commit で差し替えられる
			// （commit-reveal の完全な無効化）。
			return s.abort(ReasonProtocol)
		}
		if m.Hash == "" {
			return s.abort(ReasonProtocol) // 空ハッシュは拘束力が無い
		}
		s.peerCommit = m.Hash
		return s.maybeReveal()
	case MsgReveal:
		if s.gotPeerReveal {
			// 多層防御。commit を1通に固定した以上、2度目の reveal は
			// 同じ円にしかなりえないが、状態機械として2度目の遷移を
			// 許す理由が無い。
			return s.abort(ReasonProtocol)
		}
		if s.peerCommit == "" {
			// レビュー所見4。commit より先に reveal が届いたのは順序の
			// 入れ替わりであって、「ハッシュが合わない」という最も強い
			// 不正の断定（hash-mismatch）をすべきではない。
			return s.abort(ReasonProtocol)
		}
		nonce, err := hex.DecodeString(m.Nonce)
		if err != nil {
			return s.abort(ReasonProtocol)
		}
		c := rules.Circle{CX: m.CX, CY: m.CY, R: m.R}
		if CommitHash(c, nonce) != s.peerCommit {
			return s.abort(ReasonHashMismatch)
		}
		s.peerCircle = c
		s.gotPeerReveal = true
		return nil
	case MsgBoard:
		s.peerBoard = m.Hash
		return nil
	default:
		return s.abort(ReasonProtocol)
	}
}

// maybeReveal は両者の commit が揃っていれば reveal を送る。
func (s *Session) maybeReveal() error {
	if s.sentReveal || !s.sentCommit || s.peerCommit == "" {
		return nil
	}
	err := s.send(Message{
		T: MsgReveal, Turn: s.turn,
		CX: s.myCircle.CX, CY: s.myCircle.CY, R: s.myCircle.R,
		Nonce: hex.EncodeToString(s.myNonce),
	})
	if err != nil {
		return err
	}
	s.sentReveal = true
	return nil
}

func (s *Session) send(m Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("netplay: 送信の変換に失敗: %w", err)
	}
	return s.tr.Send(b)
}

// RequestRematch は「同じ相手ともう一度」を相手に伝える。ブロックしない。
//
// turn は終わった対戦の最終ターン。相手は検査しない（設計 §2）。
// 中断で終わった対戦では申し出られない。不正を検出した相手と再戦する理由が
// 無いためで、ここで断らないと「中断したのに再戦できる」経路ができる（設計 §6）。
func (s *Session) RequestRematch(turn int) error {
	if s.aborted != nil {
		return s.aborted
	}
	s.agreed = false
	if s.sentRematch {
		return nil
	}
	if err := s.send(Message{T: MsgRematch, Turn: turn}); err != nil {
		return err
	}
	s.sentRematch = true
	s.maybeAgreeRematch()
	return nil
}

// RematchAgreed は再戦が成立したかを返す。まだなら false ですぐ返る。
//
// 成立した時点で対戦1の状態は捨ててあるので、呼び出し側はそのまま
// 新しい対戦を始めてよい。成立を見逃さないよう、次の RequestRematch まで
// true を返し続ける。
func (s *Session) RematchAgreed() (bool, error) {
	if s.aborted != nil {
		return false, s.aborted
	}
	if s.agreed {
		return true, nil
	}
	if err := s.drain(); err != nil {
		return false, err
	}
	return s.agreed, nil
}

// maybeAgreeRematch は両者の申し出が揃っていれば対戦の境界を引く。
//
// **捨てるのは pending だけでよい。** ターンごとの状態（nonce・commit・reveal・
// 盤面ハッシュ）は、対戦2の最初の Submit が呼ぶ resetTurn が捨てる。pending は
// 逆で、resetTurn が「持ち越して再生する」ものなので、ここで捨てないと
// 対戦1の未来ターンのメッセージが対戦2で再生される。
//
// **turn を -1 に戻すことが境界そのものである。** 対戦2のターン0のメッセージは、
// こちらが Submit するより先に届きうる。合意は送ったと読んだが揃った側から成立し、
// どちらが先に揃うかは押した順と読み取りの時機で決まるので、どちらの側でも起きる。
// -1 に戻しておけば s.turn+1 として緩衝され、Submit のときに再生される。
// 戻さないと、終わった対戦のターン番号と食い違って protocol 中断になる。
//
// ここで二重に守らないのは意図的である。同じ性質を2箇所で守ると、片方が
// 壊れてももう片方が結果を同じにするので、**どちらの退行もテストで捕まえられなくなる**
// （第2段階bで同じ形の重複を1つに絞った経緯がある）。
func (s *Session) maybeAgreeRematch() {
	if !s.sentRematch || !s.gotRematch {
		return
	}
	s.pending = nil
	s.turn = -1
	s.sentRematch = false
	s.gotRematch = false
	s.agreed = true
}

// abort は相手に理由を伝えてから中断状態にする。
func (s *Session) abort(reason string) error {
	s.send(Message{T: MsgAbort, Reason: reason})
	s.aborted = AbortError{Reason: reason}
	return s.aborted
}
