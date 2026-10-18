package ui

// Emphasis はボタンの見た目の重み。
//
// **何色で塗るかではなく、どれだけ強調するかを表す。** 色を直接持たせると、
// 配色を変えるたびにここを触ることになり、「主ボタンが2つある」ような
// 意味の誤りが色の話に紛れる。
type Emphasis int

const (
	// EmphasisDisabled は今は押せないボタン。**ゼロ値をこれにしてある**。設定し忘れたボタンが
	// 黙って主として塗られるより、押せなさそうに見えるほうが気づける。
	EmphasisDisabled Emphasis = iota
	// EmphasisPrimary はその画面で最も押してほしいボタン。塗りつぶす。
	EmphasisPrimary
	// EmphasisSecondary は選べるが主ではないボタン。主より沈んだ暗い色で塗り、細い枠線を引く
	// （塗らないと暗い地の上でボタンの形が読めない）。
	EmphasisSecondary
)
