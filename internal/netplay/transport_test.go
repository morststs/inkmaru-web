package netplay

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPipeDeliversBothWays(t *testing.T) {
	a, b := NewPipe()
	defer a.Close()
	defer b.Close()

	ctx := context.Background()
	if err := a.Send([]byte("hello")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got, err := b.Receive(ctx)
	if err != nil || string(got) != "hello" {
		t.Fatalf("b が受信 = %q, %v", got, err)
	}

	if err := b.Send([]byte("world")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got, err = a.Receive(ctx)
	if err != nil || string(got) != "world" {
		t.Fatalf("a が受信 = %q, %v", got, err)
	}
}

// ゲームループから呼ぶため、届いていなければ待たずに戻れる必要がある。
func TestReceiveRespectsContext(t *testing.T) {
	a, b := NewPipe()
	defer a.Close()
	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := a.Receive(ctx)
	if err == nil {
		t.Fatal("何も送っていないのに受信できた")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, 期待 DeadlineExceeded", err)
	}
	if time.Since(start) > time.Second {
		t.Error("context の期限を無視して待ち続けた")
	}
}

func TestReceiveAfterPeerCloseReturnsErrPeerGone(t *testing.T) {
	a, b := NewPipe()
	defer a.Close()
	b.Close()

	_, err := a.Receive(context.Background())
	if !errors.Is(err, ErrPeerGone) {
		t.Errorf("err = %v, 期待 ErrPeerGone", err)
	}
}

func TestSendAfterCloseFails(t *testing.T) {
	a, b := NewPipe()
	defer b.Close()
	a.Close()
	if err := a.Send([]byte("x")); err == nil {
		t.Error("閉じた後の Send が成功した")
	}
}

// 届いているメッセージは、context が期限切れでも返る。
// select に任せると擬似ランダムになり、毎フレーム Receive を呼ぶ上位層の
// テストがフレークする。
func TestReceiveReturnsBufferedMessageEvenWhenContextExpired(t *testing.T) {
	a, b := NewPipe()
	defer a.Close()
	defer b.Close()

	if err := a.Send([]byte("hello")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 期限切れの context

	for i := 0; i < 200; i++ {
		if err := a.Send([]byte("x")); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	for i := 0; i < 200; i++ {
		got, err := b.Receive(ctx)
		if err != nil {
			t.Fatalf("%d 回目で失敗した（非決定的な select の疑い）: %v", i, err)
		}
		_ = got
	}
}

// 送信後すぐに閉じても、送った分は届く。最後の1通を落とすと実害が出る。
func TestBufferedMessagesSurvivePeerClose(t *testing.T) {
	a, b := NewPipe()
	defer b.Close()

	if err := a.Send([]byte("last")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	a.Close()

	got, err := b.Receive(context.Background())
	if err != nil {
		t.Fatalf("閉じる前に送った分が届かなかった: %v", err)
	}
	if string(got) != "last" {
		t.Errorf("受信 = %q, 期待 last", got)
	}
}

// 送信後に呼び出し側がバッファを書き換えても、受信内容は変わらない。
// 参照のまま渡す実装は、バッファを使い回す呼び出し側のもとでデータを壊す。
func TestSendCopiesTheBuffer(t *testing.T) {
	a, b := NewPipe()
	defer a.Close()
	defer b.Close()

	buf := []byte("hello")
	if err := a.Send(buf); err != nil {
		t.Fatalf("Send: %v", err)
	}
	copy(buf, "XXXXX") // 呼び出し側がバッファを使い回した

	got, err := b.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("受信 = %q, 期待 hello（送信時のバッファを参照している）", got)
	}
}
