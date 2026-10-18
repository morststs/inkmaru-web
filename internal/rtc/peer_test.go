package rtc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/morststs/inkmaru-web/internal/netplay"
)

// candidateLead は candidate を SDP より先に届かせるための待ち時間。
// host candidate の収集はこれよりずっと速いので、両者の candidate は必ず
// 相手の SetRemote より前に到着する。
const candidateLead = 500 * time.Millisecond

// 到着順が保たれない経路を模し、candidate が SDP より先に来ても繋がることを確かめる。
// 第2段階a §11 の実測により、これは想定外ではなく通常の状況である。
func TestConnectsWithCandidatesBeforeRemoteDescription(t *testing.T) {
	offerer, err := NewPeer(RoleOfferer)
	if err != nil {
		t.Fatalf("NewPeer: %v", err)
	}
	defer offerer.Close()
	answerer, err := NewPeer(RoleAnswerer)
	if err != nil {
		t.Fatalf("NewPeer: %v", err)
	}
	defer answerer.Close()

	// offerer の candidate を、answerer が SetRemote する前に流し込む
	offerer.OnLocalCandidate(func(ci Candidate) {
		answerer.AddCandidate(ci)
	})
	answerer.OnLocalCandidate(func(ci Candidate) {
		offerer.AddCandidate(ci)
	})

	offer, err := offerer.LocalDescription()
	if err != nil {
		t.Fatalf("LocalDescription: %v", err)
	}
	// わざと遅らせ、candidate が先行する状況を作る
	time.Sleep(candidateLead)
	if err := answerer.SetRemote(offer); err != nil {
		t.Fatalf("SetRemote: %v", err)
	}
	answer, err := answerer.LocalDescription()
	if err != nil {
		t.Fatalf("LocalDescription: %v", err)
	}
	// こちら側も遅らせる。片方向だけ緩衝を必要にすると、緩衝を外しても
	// 相手からの接続確認要求から peer reflexive candidate が生えて繋がってしまい、
	// テストが緩衝を守らなくなる。両方向の candidate を SDP より先に届かせる。
	time.Sleep(candidateLead)
	if err := offerer.SetRemote(answer); err != nil {
		t.Fatalf("SetRemote: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ta, err := offerer.Transport(ctx)
	if err != nil {
		t.Fatalf("offerer.Transport: %v", err)
	}
	tb, err := answerer.Transport(ctx)
	if err != nil {
		t.Fatalf("answerer.Transport: %v", err)
	}

	if err := ta.Send([]byte("commit:abc")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	rctx, rcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer rcancel()
	got, err := tb.Receive(rctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if string(got) != "commit:abc" {
		t.Errorf("受信 = %q", got)
	}
}

// 自分の candidate も、届け先が決まる前に出た分を取りこぼしてはならない。
// 収集は SetLocalDescription の直後から始まるので、呼び出し側が
// OnLocalCandidate を後から登録する順序は普通に起こりうる。
func TestLocalCandidatesEmittedBeforeHandlerAreNotLost(t *testing.T) {
	p, err := NewPeer(RoleOfferer)
	if err != nil {
		t.Fatalf("NewPeer: %v", err)
	}
	defer p.Close()

	// 先に SDP を作って収集を始めさせ、届け先はあとから決める
	if _, err := p.LocalDescription(); err != nil {
		t.Fatalf("LocalDescription: %v", err)
	}
	time.Sleep(candidateLead)

	got := make(chan Candidate, 16)
	p.OnLocalCandidate(func(ci Candidate) {
		select {
		case got <- ci:
		default:
		}
	})

	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("登録前に出た candidate が1つも渡ってこない")
	}
}

// netplay.Transport の契約その1。
// 既に届いているメッセージは、context の締切が過ぎていても優先して返さねばならない。
// netplay.Session.drain は「届いている分だけ寄越せ」の意味で期限切れの context を
// 毎フレーム渡すため、ここで context を先に見ると対戦中に何も受け取れなくなる。
//
// 素朴な select は両方が準備できていると擬似ランダムに選ぶので、1回では約半分の
// 確率で通ってしまう。多数回繰り返して、その偶然を排除する。
func TestReceiveReturnsBufferedMessageEvenWhenContextExpired(t *testing.T) {
	const trials = 200
	for i := 0; i < trials; i++ {
		tr := &dcTransport{in: make(chan []byte, 4), closed: make(chan struct{})}
		tr.in <- []byte("reveal:xyz")

		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		got, err := tr.Receive(ctx)
		cancel()
		if err != nil {
			t.Fatalf("%d 回目: Receive = %v（バッファ済みを返すべき）", i+1, err)
		}
		if string(got) != "reveal:xyz" {
			t.Fatalf("%d 回目: 受信 = %q", i+1, got)
		}
	}
}

// netplay.Transport の契約その1（続き）。
// 経路が閉じていても、閉じる前に届いていた分は先に返さねばならない。
func TestReceiveReturnsBufferedMessageEvenWhenClosed(t *testing.T) {
	const trials = 200
	for i := 0; i < trials; i++ {
		tr := &dcTransport{in: make(chan []byte, 4), closed: make(chan struct{})}
		tr.in <- []byte("reveal:xyz")
		close(tr.closed)

		got, err := tr.Receive(context.Background())
		if err != nil {
			t.Fatalf("%d 回目: Receive = %v（バッファ済みを返すべき）", i+1, err)
		}
		if string(got) != "reveal:xyz" {
			t.Fatalf("%d 回目: 受信 = %q", i+1, got)
		}
	}
}

// netplay.Transport の契約その2。
// 何も届かないまま締切が来たときのエラーは context.DeadlineExceeded か
// context.Canceled を満たさねばならない。それ以外は drain に致命的エラーとして
// 扱われ、何も起きていない毎フレームの Poll が対戦を終わらせてしまう。
func TestReceiveTimeoutIsContextError(t *testing.T) {
	tr := &dcTransport{in: make(chan []byte, 4), closed: make(chan struct{})}

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := tr.Receive(ctx)
	if err == nil {
		t.Fatal("締切超過で err = nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Errorf("Receive = %v（context.DeadlineExceeded か context.Canceled を満たすべき）", err)
	}
	if errors.Is(err, netplay.ErrPeerGone) {
		t.Error("届いていないだけの状態を ErrPeerGone として返している")
	}
}

// 経路が閉じたあと、バッファも空なら ErrPeerGone を返す。
func TestReceiveAfterCloseIsPeerGone(t *testing.T) {
	tr := &dcTransport{in: make(chan []byte, 4), closed: make(chan struct{})}
	close(tr.closed)

	if _, err := tr.Receive(context.Background()); !errors.Is(err, netplay.ErrPeerGone) {
		t.Errorf("Receive = %v（netplay.ErrPeerGone を期待）", err)
	}
	if err := tr.Send([]byte("x")); !errors.Is(err, netplay.ErrPeerGone) {
		t.Errorf("Send = %v（netplay.ErrPeerGone を期待）", err)
	}
}

// dcTransport が netplay.Transport を満たすことを型で固定する。
var _ netplay.Transport = (*dcTransport)(nil)

// connectPair は繋がった Peer の対を返す。candidate は両方向とも相手の SetRemote より
// 先に届くようにしてあるので、緩衝が効いていなければここで繋がらない。
// inject は最初の candidate を渡す直前に一度だけ呼ばれる差し込み口で、
// 本物より先に何かを溜めさせたいテストが使う。両者の SetRemote より前に呼ばれる。
func connectPair(t *testing.T, inject func(offerer, answerer *Peer)) (*Peer, *Peer) {
	t.Helper()

	offerer, err := NewPeer(RoleOfferer)
	if err != nil {
		t.Fatalf("NewPeer: %v", err)
	}
	t.Cleanup(func() { offerer.Close() })
	answerer, err := NewPeer(RoleAnswerer)
	if err != nil {
		t.Fatalf("NewPeer: %v", err)
	}
	t.Cleanup(func() { answerer.Close() })

	var once sync.Once
	offerer.OnLocalCandidate(func(ci Candidate) {
		if inject != nil {
			once.Do(func() { inject(offerer, answerer) })
		}
		answerer.AddCandidate(ci)
	})
	answerer.OnLocalCandidate(func(ci Candidate) {
		offerer.AddCandidate(ci)
	})

	offer, err := offerer.LocalDescription()
	if err != nil {
		t.Fatalf("LocalDescription: %v", err)
	}
	time.Sleep(candidateLead)
	if err := answerer.SetRemote(offer); err != nil {
		t.Fatalf("answerer.SetRemote: %v", err)
	}
	answer, err := answerer.LocalDescription()
	if err != nil {
		t.Fatalf("LocalDescription: %v", err)
	}
	time.Sleep(candidateLead)
	if err := offerer.SetRemote(answer); err != nil {
		t.Fatalf("offerer.SetRemote: %v", err)
	}
	return offerer, answerer
}

// 相手が Transport() を呼ぶより先に届いたメッセージを取りこぼしてはならない。
//
// pion は DataChannel が開いた時点で読み取りを回し始め、OnMessage が未登録の
// メッセージは黙って捨てる（webrtc/datachannel.go の onMessage は handler が nil なら
// 何もしない）。OnMessage を Transport() の中で仕掛けると、相手が先に送って
// こちらが1フレーム遅れただけで第1ターンの commit が消え、commit-reveal は
// 揃わないまま無言で固まる。しかも原因が相手の時間切れに見える。
func TestMessageSentBeforeTransportIsCalledArrives(t *testing.T) {
	offerer, answerer := connectPair(t, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ta, err := offerer.Transport(ctx)
	if err != nil {
		t.Fatalf("offerer.Transport: %v", err)
	}
	if err := ta.Send([]byte("commit:first")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// 相手はまだ Transport() を呼んでいない。ここで捨てられてはならない。
	time.Sleep(1500 * time.Millisecond)

	tb, err := answerer.Transport(ctx)
	if err != nil {
		t.Fatalf("answerer.Transport: %v", err)
	}
	rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rcancel()
	got, err := tb.Receive(rctx)
	if err != nil {
		t.Fatalf("Receive: %v（Transport より先に届いた分が消えている）", err)
	}
	if string(got) != "commit:first" {
		t.Errorf("受信 = %q", got)
	}
}

// 壊れた candidate が1つ混ざっても、残りの正しい candidate で繋がらねばならない。
//
// candidate は公開のシグナリング経由で、こちらが制御できない相手から届く。
// 1つでも解釈できなければ全部やめる実装だと、壊れた文字列を1つ送るだけで
// 対戦を確実に潰せてしまう。ICE は「使える candidate が1つでもあれば繋がる」設計である。
func TestMalformedCandidateDoesNotKillTheMatch(t *testing.T) {
	offerer, answerer := connectPair(t, func(_, answerer *Peer) {
		// 本物より先に、絶対に解釈できないものを溜めさせる
		if err := answerer.AddCandidate(Candidate{Candidate: "これは candidate ではない"}); err != nil {
			t.Errorf("壊れた candidate で AddCandidate がエラーを返した: %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ta, err := offerer.Transport(ctx)
	if err != nil {
		t.Fatalf("offerer.Transport: %v", err)
	}
	tb, err := answerer.Transport(ctx)
	if err != nil {
		t.Fatalf("answerer.Transport: %v（壊れた candidate 1つで接続が潰れた）", err)
	}
	if err := ta.Send([]byte("commit:abc")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	rctx, rcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer rcancel()
	if got, err := tb.Receive(rctx); err != nil || string(got) != "commit:abc" {
		t.Fatalf("Receive = %q, %v", got, err)
	}

	// 遠隔記述の設定後（即時適用の経路）でも、壊れた1つは致命的であってはならない。
	if err := answerer.AddCandidate(Candidate{Candidate: "これも candidate ではない"}); err != nil {
		t.Errorf("即時適用の経路で壊れた candidate がエラーになった: %v", err)
	}

	// 「繋がったから通った」だけでは、壊れた分を飛ばしたのか、そもそも
	// 溜めた分を一切使っていないのか区別できない。飛ばした数を直接見る。
	answerer.mu.Lock()
	rejected := answerer.rejected
	answerer.mu.Unlock()
	if rejected != 2 {
		t.Errorf("飛ばした candidate の数 = %d（緩衝経路と即時経路で1つずつ、計2を期待）", rejected)
	}
}

// 溜める candidate には上限が要る。中身は相手から来る外部入力だからである。
// また Close したら手放さねばならない。長寿命のタブで対戦を繰り返す前提なので、
// 終わった Peer が外部入力を抱えたまま残るのは避ける。
func TestPendingCandidatesAreCappedAndClearedOnClose(t *testing.T) {
	p, err := NewPeer(RoleAnswerer)
	if err != nil {
		t.Fatalf("NewPeer: %v", err)
	}
	defer p.Close()

	const flood = 10000

	// 同じものは1つに畳む。同じ candidate を繰り返し送りつける手が
	// 最も仕掛けやすいので、そこを塞ぐ。
	for i := 0; i < flood; i++ {
		if err := p.AddCandidate(floodCandidate(0)); err != nil {
			t.Fatalf("AddCandidate: %v", err)
		}
	}
	p.mu.Lock()
	n := len(p.pending)
	p.mu.Unlock()
	if n != 1 {
		t.Errorf("同じ candidate を %d 件送ったあとの数 = %d（1件に畳まれるべき）", flood, n)
	}

	// 相異なるものは畳めない。上限で頭打ちになり、かつ新しいほうが残ること。
	// 新しいほうを捨てると、役に立たない candidate を先に送りつけるだけで
	// あとから来る本物を締め出せてしまう。
	for i := 1; i <= flood; i++ {
		if err := p.AddCandidate(floodCandidate(i)); err != nil {
			t.Fatalf("AddCandidate: %v", err)
		}
	}
	p.mu.Lock()
	n = len(p.pending)
	newest := p.pending[len(p.pending)-1]
	p.mu.Unlock()
	if n != maxPendingCandidates {
		t.Errorf("相異なる candidate を %d 件送ったあとの数 = %d（上限 %d で頭打ちになるべき）",
			flood, n, maxPendingCandidates)
	}
	if newest.key() != floodCandidate(flood).key() {
		t.Errorf("最後に溜めたのは %q（最新の1件が残るべき）", newest.Candidate)
	}

	// 自分の candidate 側も同じく上限が要る
	p.mu.Lock()
	p.localPending = make([]Candidate, 3)
	p.mu.Unlock()

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending != nil {
		t.Errorf("Close 後も相手の candidate が %d 件残っている", len(p.pending))
	}
	if p.localPending != nil {
		t.Errorf("Close 後も自分の candidate が %d 件残っている", len(p.localPending))
	}
}

// DataChannel が閉じたら、Transport() を呼んだあとでも Peer は「相手が消えた」と分かること。
//
// pion の DataChannel.OnClose はハンドラを1つしか持たない（代入であって追加ではない）。
// Transport を作るときに仕掛け直すと Peer 側の検知が消え、ICE の状態遷移という
// 何倍も遅い経路まで落ちてしまう。SCTP の即時通知を捨てない。
func TestDataChannelCloseMarksPeerGoneAfterTransport(t *testing.T) {
	offerer, answerer := connectPair(t, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := offerer.Transport(ctx); err != nil {
		t.Fatalf("offerer.Transport: %v", err)
	}
	tb, err := answerer.Transport(ctx)
	if err != nil {
		t.Fatalf("answerer.Transport: %v", err)
	}

	// 相手側から DataChannel だけを閉じる（PeerConnection は開けたまま）
	if err := tb.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// ICE の切断判定はこれよりずっと遅い。ここで拾えるなら SCTP の通知で拾っている。
	select {
	case <-offerer.gone:
	case <-time.After(3 * time.Second):
		t.Fatal("DataChannel が閉じても Peer が相手の消失に気づかない")
	}
}

// 後片付けのつもりで OnLocalCandidate(nil) を呼んでも、溜めた自分の candidate を捨てないこと。
func TestOnLocalCandidateNilKeepsBufferedCandidates(t *testing.T) {
	p, err := NewPeer(RoleOfferer)
	if err != nil {
		t.Fatalf("NewPeer: %v", err)
	}
	defer p.Close()

	if _, err := p.LocalDescription(); err != nil {
		t.Fatalf("LocalDescription: %v", err)
	}
	time.Sleep(candidateLead)

	p.OnLocalCandidate(nil) // 届け先を外す。ここで溜めた分が消えてはならない。

	got := make(chan Candidate, 16)
	p.OnLocalCandidate(func(ci Candidate) {
		select {
		case got <- ci:
		default:
		}
	})
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("nil を挟んだせいで、溜めた自分の candidate が消えた")
	}
}

// pion の型との変換で値が落ちないこと。公開 API を pion 非依存にした代償が
// 「変換で欠ける」ことであってはならない。
func TestDescriptionAndCandidateRoundTripThroughPion(t *testing.T) {
	for _, typ := range []string{"offer", "answer", "pranswer", "rollback"} {
		d := Description{Type: typ, SDP: "v=0\r\n"}
		if got := descriptionFromPion(d.toPion()); got != d {
			t.Errorf("Description の往復 = %+v, 元 = %+v", got, d)
		}
	}

	mid := "0"
	idx := uint16(0)
	ufrag := "abcd"
	c := Candidate{
		Candidate:        "candidate:1 1 udp 2130706431 127.0.0.1 1234 typ host",
		SDPMid:           &mid,
		SDPMLineIndex:    &idx,
		UsernameFragment: &ufrag,
	}
	got := candidateFromPion(c.toPion())
	if got.Candidate != c.Candidate || got.SDPMid != c.SDPMid ||
		got.SDPMLineIndex != c.SDPMLineIndex || got.UsernameFragment != c.UsernameFragment {
		t.Errorf("Candidate の往復 = %+v, 元 = %+v", got, c)
	}
}

// floodCandidate は解釈できるが役に立たない candidate を作る。
// 192.0.2.0/24 は文書用に予約された到達しない網なので、これで繋がることはない。
func floodCandidate(n int) Candidate {
	mid := "0"
	idx := uint16(0)
	return Candidate{
		Candidate:     fmt.Sprintf("candidate:1 1 udp 2130706431 192.0.2.1 %d typ host", 10000+n),
		SDPMid:        &mid,
		SDPMLineIndex: &idx,
	}
}

// 緩衝が溢れても、あとから届いた本物の candidate で繋がらねばならない。
//
// 上限を設けること自体は正しいが、溢れたときに「新しいほうを捨てる」と、
// 役に立たない candidate を上限ぶん先に送りつけるだけで、あとから来る本物を
// 1つ残らず締め出せてしまう。無制限（記憶を食う迷惑）を、確実な対戦の妨害に
// すり替えることになり、そちらのほうが悪い。
// 溢れたときは古いほうを捨て、同じものは1つに畳む。
func TestFloodedPeerStillConnects(t *testing.T) {
	cases := []struct {
		name string
		fill func(p *Peer)
	}{
		{
			// 同じものの繰り返し。仕掛けるのが最も簡単なので、まずこれを畳む。
			name: "同一の candidate を上限の4倍",
			fill: func(p *Peer) {
				for i := 0; i < maxPendingCandidates*4; i++ {
					p.AddCandidate(floodCandidate(0))
				}
			},
		},
		{
			// 全部異なる場合。畳めないので、古いほうから捨てて場所を空ける。
			name: "相異なる candidate を上限の4倍",
			fill: func(p *Peer) {
				for i := 0; i < maxPendingCandidates*4; i++ {
					p.AddCandidate(floodCandidate(i))
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			offerer, answerer := connectPair(t, func(o, a *Peer) {
				// 本物より先に、両者の緩衝を埋める
				tc.fill(o)
				tc.fill(a)
			})

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			ta, err := offerer.Transport(ctx)
			if err != nil {
				t.Fatalf("offerer.Transport: %v（緩衝が溢れたせいで本物が締め出された）", err)
			}
			tb, err := answerer.Transport(ctx)
			if err != nil {
				t.Fatalf("answerer.Transport: %v（緩衝が溢れたせいで本物が締め出された）", err)
			}
			if err := ta.Send([]byte("commit:abc")); err != nil {
				t.Fatalf("Send: %v", err)
			}
			rctx, rcancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer rcancel()
			if got, err := tb.Receive(rctx); err != nil || string(got) != "commit:abc" {
				t.Fatalf("Receive = %q, %v", got, err)
			}

			// 上限そのものは効いていること（無制限に戻して通したのではない）
			for _, p := range []*Peer{offerer, answerer} {
				p.mu.Lock()
				n := len(p.pending)
				p.mu.Unlock()
				if n > maxPendingCandidates {
					t.Errorf("溜まった数 = %d（上限 %d を超えている）", n, maxPendingCandidates)
				}
			}
		})
	}
}

// DataChannel は最初の1本だけを見ること。
//
// 2本目にも受信を仕掛けると、誰も持っていない Transport にメッセージが流れ込む。
// さらに悪いことに OnClose が貼り替わり、閉じるのは2本目なのに死ぬのは1本目、
// という筋の通らない結びつきができる。ラベルが1本に固定されている今は
// 起こらないが、ラベルを増やす人のために罠を残さない。
func TestSecondDataChannelIsIgnored(t *testing.T) {
	offerer, answerer := connectPair(t, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ta, err := offerer.Transport(ctx)
	if err != nil {
		t.Fatalf("offerer.Transport: %v", err)
	}
	tb, err := answerer.Transport(ctx)
	if err != nil {
		t.Fatalf("answerer.Transport: %v", err)
	}

	answerer.mu.Lock()
	first := answerer.tr
	answerer.mu.Unlock()

	// 2本目を開く。answerer 側の OnDataChannel がもう一度発火する。
	opened := make(chan struct{})
	extra, err := offerer.pc.CreateDataChannel("inkmaru-extra", nil)
	if err != nil {
		t.Fatalf("CreateDataChannel: %v", err)
	}
	extra.OnOpen(func() { close(opened) })
	select {
	case <-opened:
	case <-time.After(10 * time.Second):
		t.Fatal("2本目が開かない")
	}

	answerer.mu.Lock()
	got := answerer.tr
	answerer.mu.Unlock()
	if got != first {
		t.Error("2本目の DataChannel が受け皿を乗っ取った")
	}

	// 2本目を閉じる。1本目は無事でなければならない。
	if err := extra.Close(); err != nil {
		t.Fatalf("extra.Close: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	select {
	case <-answerer.gone:
		t.Fatal("2本目を閉じただけで Peer が相手の消失と判断した")
	default:
	}
	if err := ta.Send([]byte("commit:still-alive")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rcancel()
	if got, err := tb.Receive(rctx); err != nil || string(got) != "commit:still-alive" {
		t.Fatalf("Receive = %q, %v（2本目を閉じたら1本目まで死んだ）", got, err)
	}
}
