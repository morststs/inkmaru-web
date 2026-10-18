package netplay

import (
	"context"
	"errors"
	"sync"
)

// ErrPeerGone は相手との経路が失われたことを表す。
var ErrPeerGone = errors.New("netplay: 相手との接続が失われた")

// Transport は相手との1本の双方向経路。
//
// 実装は WebRTC の DataChannel だが、このインタフェースは WebRTC を前提にしない。
// Receive が context を取るのは、ゲームループから呼ぶときに待たずに済ませるためである。
//
// Receive の契約（netplay.Session.drain が依存する。レビュー task-3-review.md 所見5）:
//   - 締切までに何も届かなければ、errors.Is(err, context.DeadlineExceeded) または
//     errors.Is(err, context.Canceled) を満たすエラーを返すこと。どちらであっても
//     「届いていないだけ」を意味する。この2つ以外のエラーは drain によって
//     致命的エラー（対戦の中断）として扱われる。
//   - 締切に既に達している・過ぎている context を渡された場合でも、その時点で
//     既に届いている（バッファ済みの）メッセージがあれば、締切の状態によらず
//     優先してそれを返すこと。drain は毎フレーム呼ばれる Poll から性能上の理由で
//     期限0の context を渡すため、この前倒しの配送が無いと、たとえ相手から
//     何か届いていても永遠に「届いていない」に分類されてしまう。
//
// 実装がこの契約を満たさないと、何も起きていない毎フレームの Poll が
// 対戦を止めてしまう（s.aborted は一度設定されると復帰しない）。
type Transport interface {
	Send(b []byte) error
	Receive(ctx context.Context) ([]byte, error)
	Close() error
}

// pipe はメモリ上の Transport。テストで2つの Session をつなぐために使う。
type pipe struct {
	out       chan []byte
	in        chan []byte
	closed    chan struct{}
	closeOnce *sync.Once
}

// NewPipe は互いにつながった2つの Transport を返す。
func NewPipe() (Transport, Transport) {
	ab := make(chan []byte, 256)
	ba := make(chan []byte, 256)
	closed := make(chan struct{})
	once := &sync.Once{}
	return &pipe{out: ab, in: ba, closed: closed, closeOnce: once}, &pipe{out: ba, in: ab, closed: closed, closeOnce: once}
}

func (p *pipe) Send(b []byte) error {
	select {
	case <-p.closed:
		return ErrPeerGone
	default:
	}
	cp := append([]byte(nil), b...)
	select {
	case p.out <- cp:
		return nil
	case <-p.closed:
		return ErrPeerGone
	}
}

func (p *pipe) Receive(ctx context.Context) ([]byte, error) {
	// 先に非ブロッキングで覗く。届いているものは context の状態によらず返す。
	// 呼び出し側は「期限切れの context」を「届いている分だけ返せ」の意味で使う。
	// ここで select に任せると、届いていても擬似ランダムに ctx.Err() を返すことがあり、
	// 毎フレーム呼ぶ上位層のテストがフレークする。
	select {
	case b := <-p.in:
		return b, nil
	default:
	}
	select {
	case b := <-p.in:
		return b, nil
	case <-p.closed:
		return nil, ErrPeerGone
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *pipe) Close() error {
	p.closeOnce.Do(func() { close(p.closed) })
	return nil
}
