package signaling

import (
	"crypto/rand"
	"math/big"
	"strings"
)

// CodeAlphabet は部屋番号に使う文字。
// 0/O と 1/I/L を除いてある。番号は口頭やチャットで人が伝えるものなので、
// 桁数を節約するより読み違いを減らすほうが重要である。
const CodeAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// CodeLength は部屋番号の桁数。31^6 で約8.87億通り。
const CodeLength = 6

// NewCode は部屋番号を1つ作る。
// crypto/rand から一様に選ぶ。剰余を使うと文字集合の長さが2の冪でないぶん
// 偏りが出るため、big.Int の Int を使って偏りのない範囲で引く。
func NewCode() (string, error) {
	n := big.NewInt(int64(len(CodeAlphabet)))
	b := make([]byte, CodeLength)
	for i := range b {
		v, err := rand.Int(rand.Reader, n)
		if err != nil {
			return "", err
		}
		b[i] = CodeAlphabet[v.Int64()]
	}
	return string(b), nil
}

// IsValidCode は code が部屋番号として成立しうる形かどうかを返す。
// 桁数が CodeLength と異なるか、CodeAlphabet に無い文字を含む場合は false。
//
// 実在確認（DynamoDB 参照）の前に形だけで弾くためのものである。形が違う
// 時点で部屋が存在するはずがなく、無駄な読み取りを避けられる。またクライアントに
// 返すエラーは実在しない場合と同じ room-not-found にする。形式違反かどうかを
// 区別して返すと、有効な番号を探るための手がかりを与えてしまう。
func IsValidCode(code string) bool {
	if len(code) != CodeLength {
		return false
	}
	for i := 0; i < len(code); i++ {
		c := code[i]
		if !strings.ContainsRune(CodeAlphabet, rune(c)) {
			return false
		}
	}
	return true
}
