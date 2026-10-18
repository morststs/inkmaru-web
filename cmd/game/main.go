// Command game は Inkmaru を起動する。
package main

import (
	"log"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/morststs/inkmaru-web/internal/gfx"
	"github.com/morststs/inkmaru-web/internal/rules"
)

func main() {
	ebiten.SetWindowTitle("Inkmaru")
	ebiten.SetWindowSize(390, 844)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	if err := ebiten.RunGame(gfx.NewGame(rules.DefaultConfig())); err != nil {
		log.Fatal(err)
	}
}
