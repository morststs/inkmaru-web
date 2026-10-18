package netplay

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"

	"github.com/morststs/inkmaru-web/internal/rules"
)

// NonceLen は commit に混ぜる乱数の長さ。
//
// 盤面の判定格子は 256×256、半径の候補も高々100通り程度なので、
// 円の候補は約655万通りしかない。nonce が無いハッシュは総当たりで一瞬で割れ、
// 相手の commit を受け取った時点で中身が分かってしまう。
// 16バイトあれば候補が 2^128 倍になり、総当たりは不可能になる。
const NonceLen = 16

func NewNonce() ([]byte, error) {
	b := make([]byte, NonceLen)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// CommitHash は円と nonce から commit のハッシュを作る。
//
// float64 は Float64bits でビット表現にし、固定長8バイトのビッグエンディアンで並べる。
// 文字列に変換して連結すると、桁数や指数表記の差で環境によって値が揺れうる。
func CommitHash(c rules.Circle, nonce []byte) string {
	var b bytes.Buffer
	for _, v := range []float64{c.CX, c.CY, c.R} {
		binary.Write(&b, binary.BigEndian, math.Float64bits(v))
	}
	b.Write(nonce)
	sum := sha256.Sum256(b.Bytes())
	return hex.EncodeToString(sum[:])
}

// BoardHash は盤面の状態からハッシュを作る。
//
// P2P には審判がいないため、両者が独立に計算した盤面が食い違っても誰も裁定できない。
// 毎ターン交換して不一致を検出する。所有者を1セル1バイトで左上から並べる。
// Board の内部表現は非公開なので、公開メソッドの OwnerAt で読む。
func BoardHash(b *rules.Board) string {
	n := b.GridN()
	buf := make([]byte, 0, n*n)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			buf = append(buf, byte(b.OwnerAt(x, y)))
		}
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}
