package sigclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
)

func TestParseCreated(t *testing.T) {
	e, err := parseEvent([]byte(`{"type":"created","room":"P2Z5BS"}`))
	if err != nil {
		t.Fatalf("parseEvent: %v", err)
	}
	if e.Kind != EventCreated || e.Room != "P2Z5BS" {
		t.Errorf("解釈結果 = %+v", e)
	}
}

func TestParseSignalKeepsDataOpaque(t *testing.T) {
	raw := `{"type":"signal","data":{"type":"offer","sdp":"v=0\r\n"}}`
	e, err := parseEvent([]byte(raw))
	if err != nil {
		t.Fatalf("parseEvent: %v", err)
	}
	if e.Kind != EventSignal {
		t.Fatalf("種別 = %v", e.Kind)
	}
	var back map[string]any
	if err := json.Unmarshal(e.Data, &back); err != nil {
		t.Fatalf("data が JSON として保たれていない: %v", err)
	}
	if back["sdp"] != "v=0\r\n" {
		t.Errorf("data が改変された: %v", back)
	}
}

func TestParseError(t *testing.T) {
	e, err := parseEvent([]byte(`{"type":"error","code":"room-not-found"}`))
	if err != nil {
		t.Fatalf("parseEvent: %v", err)
	}
	if e.Kind != EventError || e.Code != "room-not-found" {
		t.Errorf("解釈結果 = %+v", e)
	}
}

func TestParseUnknownTypeIsError(t *testing.T) {
	if _, err := parseEvent([]byte(`{"type":"explode"}`)); err == nil {
		t.Error("未知の種別を受け入れた")
	}
}

func TestParseBrokenJSONIsError(t *testing.T) {
	if _, err := parseEvent([]byte(`{"type":`)); err == nil {
		t.Error("壊れた JSON を受け入れた")
	}
}

func TestPeerLeftAndJoined(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want EventKind
	}{
		{`{"type":"joined","room":"ABC123"}`, EventJoined},
		{`{"type":"peer-joined"}`, EventPeerJoined},
		{`{"type":"peer-left"}`, EventPeerLeft},
	} {
		e, err := parseEvent([]byte(c.raw))
		if err != nil {
			t.Fatalf("%s: %v", c.raw, err)
		}
		if e.Kind != c.want {
			t.Errorf("%s -> %v, 期待 %v", c.raw, e.Kind, c.want)
		}
	}
}

// fakeWire は WebSocket の代わりにバイト列を出し入れする偽の経路。
// 実接続なしで Next と送信の組み立てを検証するために使う。
//
// 受信は Client が別の goroutine で回すので、この偽物も並行に触られる。
// 錠を持たせないと -race で落ちる。
type fakeWire struct {
	mu   sync.Mutex
	in   [][]byte // 受信する順に並べた通知
	err  error    // in を使い切ったあとに返す転送エラー
	sent [][]byte // 送信されたバイト列
}

func (w *fakeWire) read(context.Context) ([]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.in) == 0 {
		if w.err != nil {
			return nil, w.err
		}
		return nil, io.EOF
	}
	b := w.in[0]
	w.in = w.in[1:]
	return b, nil
}

func (w *fakeWire) write(_ context.Context, b []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sent = append(w.sent, append([]byte(nil), b...))
	return nil
}

// sentCopy は送信済みのバイト列を写して返す。受信 goroutine と同じ錠で守る。
func (w *fakeWire) sentCopy() [][]byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([][]byte(nil), w.sent...)
}

func (w *fakeWire) client() *Client { return newClientWith(w.read, w.write) }

// 解釈できない1通が届いても対戦を殺してはならない。
// 網から来る以上、未知の種別も壊れた JSON も起こりうる。
func TestNextSurvivesUnreadableMessage(t *testing.T) {
	w := &fakeWire{in: [][]byte{
		[]byte(`{"type":"explode"}`),
		[]byte(`{"type":`),
		[]byte(`{"type":"peer-joined"}`),
	}}
	c := w.client()
	ctx := context.Background()

	for i, want := range []string{"未知の種別", "壊れた JSON"} {
		if _, err := c.Next(ctx); !errors.Is(err, ErrUnreadable) {
			t.Fatalf("%d通目（%s）のエラー = %v, ErrUnreadable を包んでいない", i+1, want, err)
		}
	}
	e, err := c.Next(ctx)
	if err != nil {
		t.Fatalf("解釈できない通知のあとで受信できなくなった: %v", err)
	}
	if e.Kind != EventPeerJoined {
		t.Errorf("種別 = %v, 期待 EventPeerJoined", e.Kind)
	}
}

// サーバからの error はデータであって転送の失敗ではない。
// 接続が切れたのと区別できなければ、画面は「部屋がない」を「通信断」と誤って伝える。
func TestServerErrorIsDataNotTransportFailure(t *testing.T) {
	w := &fakeWire{
		in:  [][]byte{[]byte(`{"type":"error","code":"room-full"}`)},
		err: io.ErrUnexpectedEOF,
	}
	c := w.client()
	ctx := context.Background()

	e, err := c.Next(ctx)
	if err != nil {
		t.Fatalf("error 通知でエラーが返った: %v", err)
	}
	if e.Kind != EventError || e.Code != "room-full" {
		t.Fatalf("解釈結果 = %+v", e)
	}

	// 続く本物の転送エラーは、解釈の失敗とは別物として届かなければならない。
	if _, err := c.Next(ctx); err == nil {
		t.Fatal("転送エラーが返らなかった")
	} else if errors.Is(err, ErrUnreadable) {
		t.Errorf("転送エラーが解釈の失敗として扱われた: %v", err)
	} else if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("元のエラーが包まれていない: %v", err)
	}
}

// 到着順は保たれない（第2段階a §11 の実測）。Next は届いた順にそのまま渡すだけで、
// 並べ替えも先読みもしてはならない。順序の判断は呼び出し側に委ねる。
func TestNextDeliversInArrivalOrderWithoutReordering(t *testing.T) {
	w := &fakeWire{in: [][]byte{
		[]byte(`{"type":"signal","data":{"n":1}}`),
		[]byte(`{"type":"peer-joined"}`),
		[]byte(`{"type":"signal","data":{"n":2}}`),
	}}
	c := w.client()
	ctx := context.Background()

	want := []EventKind{EventSignal, EventPeerJoined, EventSignal}
	for i, k := range want {
		e, err := c.Next(ctx)
		if err != nil {
			t.Fatalf("%d通目: %v", i+1, err)
		}
		if e.Kind != k {
			t.Fatalf("%d通目の種別 = %v, 期待 %v", i+1, e.Kind, k)
		}
	}
}

// 送信するバイト列は配備済みサーバの受け口と一字一句合っていなければならない。
func TestRequestsMatchServerWireFormat(t *testing.T) {
	w := &fakeWire{}
	c := w.client()
	ctx := context.Background()

	if err := c.Create(ctx); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := c.Join(ctx, "K7M2QX"); err != nil {
		t.Fatalf("Join: %v", err)
	}
	if err := c.SendSignal(ctx, map[string]string{"type": "offer", "sdp": "v=0"}); err != nil {
		t.Fatalf("SendSignal: %v", err)
	}

	want := []string{
		`{"action":"create"}`,
		`{"action":"join","room":"K7M2QX"}`,
		`{"action":"signal","data":{"sdp":"v=0","type":"offer"}}`,
	}
	sent := w.sentCopy()
	if len(sent) != len(want) {
		t.Fatalf("送信数 = %d, 期待 %d: %q", len(sent), len(want), sent)
	}
	for i, s := range want {
		if string(sent[i]) != s {
			t.Errorf("%d通目 = %s, 期待 %s", i+1, sent[i], s)
		}
	}
}

// data に符号化できない値が来ても、壊れたバイト列を送らずに失敗すること。
func TestSendSignalRejectsUnencodableData(t *testing.T) {
	w := &fakeWire{}
	c := w.client()
	if err := c.SendSignal(context.Background(), func() {}); err == nil {
		t.Error("符号化できない data を受け入れた")
	}
	if sent := w.sentCopy(); len(sent) != 0 {
		t.Errorf("失敗したのに送信された: %q", sent)
	}
}

// 接続を持たない Client の Close で落ちないこと。
func TestCloseWithoutConn(t *testing.T) {
	if err := (&fakeWire{}).client().Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// 1回の Next を諦めても、接続を殺してはならない。
//
// coder/websocket は Read/Write に渡した context が切れると接続そのものを閉じる。
// 「この1通は3秒待って諦める」つもりで期限付きの context を渡すのはごく自然な
// 書き方なので、そのまま実接続へ流すと対戦の途中で回線が死ぬ。Client は
// 呼び出し側の context を「待つのをやめる」ためだけに使い、実際の読み書きには
// 接続の生存期間の context を渡す。
func TestCancellingOneNextDoesNotKillTheConnection(t *testing.T) {
	got := make(chan context.Context, 4)
	release := make(chan []byte)
	read := func(ctx context.Context) ([]byte, error) {
		got <- ctx
		b, ok := <-release
		if !ok {
			return nil, io.EOF
		}
		return b, nil
	}
	c := newClientWith(read, func(context.Context, []byte) error { return nil })
	defer c.Close()

	// 受信 goroutine が読みに入るまで待ち、渡された context を確かめる。
	readCtx := <-got
	if readCtx.Err() != nil {
		t.Fatalf("受信に渡された context が既に切れている: %v", readCtx.Err())
	}

	// 呼び出し側は1通目を諦める。
	ctx1, cancel1 := context.WithCancel(context.Background())
	cancel1()
	if _, err := c.Next(ctx1); !errors.Is(err, context.Canceled) {
		t.Fatalf("諦めた Next のエラー = %v, context.Canceled を期待", err)
	}

	// 諦めたことで受信側の context まで切れていてはならない。
	if readCtx.Err() != nil {
		t.Fatalf("1回の Next を諦めただけで接続の context が切れた: %v", readCtx.Err())
	}

	// 続きは普通に受け取れる。
	release <- []byte(`{"type":"peer-joined"}`)
	e, err := c.Next(context.Background())
	if err != nil {
		t.Fatalf("諦めたあとに受信できなくなった: %v", err)
	}
	if e.Kind != EventPeerJoined {
		t.Errorf("種別 = %v, 期待 EventPeerJoined", e.Kind)
	}
}

// 送信の context も同じ理由で実接続へ流してはならない。
func TestSendDoesNotPassCallerContextToTheWire(t *testing.T) {
	var wireCtx context.Context
	w := func(ctx context.Context, _ []byte) error { wireCtx = ctx; return nil }
	c := newClientWith(func(ctx context.Context) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() }, w)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := c.Create(ctx); err != nil {
		t.Fatalf("Create: %v", err)
	}
	cancel()
	if wireCtx == nil {
		t.Fatal("書き込みが呼ばれていない")
	}
	if wireCtx.Err() != nil {
		t.Errorf("呼び出し側の context をそのまま経路へ渡している: %v", wireCtx.Err())
	}
}

// Close は何度呼んでも同じ結果を返す。
// defer で閉じつつ明示的にも閉じる書き方で、実害が無いのに記録が汚れるのを避ける。
func TestCloseIsIdempotent(t *testing.T) {
	c := (&fakeWire{}).client()
	first := c.Close()
	if second := c.Close(); second != first {
		t.Errorf("2回目の Close = %v, 1回目 = %v（同じであるべき）", second, first)
	}
}
