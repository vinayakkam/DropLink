// Command genicon draws DropLink's app icon as pixel art and writes it as a
// 1024x1024 PNG, the source for `tauri icon`. Two nodes joined by a beam
// with a data packet in flight, over a dark rounded tile.
// Run from the repo root: go run ./tools/genicon
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
)

var art = []string{
	"..kkkkkkkkkkkk..",
	".kkkkkkkkkkkkkk.",
	"kkwkkkkkkkkkCkkk",
	"kkkkkkkCkkkkkkkk",
	"kkkkkkkkkkkkkkkk",
	"kkcckkkkkkkkmmkk",
	"kcwwcCkaakCmwwmk",
	"kcwwcCkaakCmwwmk",
	"kkcckkkkkkkkmmkk",
	"kkkkkkkkkkkkkkkk",
	"kkkkkkkkkkkkkkkk",
	"kkccccccccCCCCkk",
	"kkkkkkkkkkkkkkkk",
	"kkkkkCkkkkkkwkkk",
	".kkkkkkkkkkkkkk.",
	"..kkkkkkkkkkkk..",
}

var palette = map[byte]color.NRGBA{
	'.': {0, 0, 0, 0},
	'k': {0x0b, 0x0f, 0x1a, 0xff}, // void
	'c': {0x3e, 0xf2, 0xff, 0xff}, // cyan
	'C': {0x13, 0x6e, 0x8a, 0xff}, // dim cyan
	'm': {0xff, 0x3e, 0xa5, 0xff}, // magenta
	'w': {0xe8, 0xfb, 0xff, 0xff}, // white core
	'a': {0xff, 0xb6, 0x3e, 0xff}, // amber packet
}

func main() {
	const size, scale = 1024, 64
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y, row := range art {
		for x := 0; x < len(row); x++ {
			c, ok := palette[row[x]]
			if !ok {
				fmt.Fprintf(os.Stderr, "genicon: unknown color %q at %d,%d\n", row[x], x, y)
				os.Exit(1)
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.SetNRGBA(x*scale+dx, y*scale+dy, c)
				}
			}
		}
	}
	out := filepath.Join("desktop", "app-icon.png")
	f, err := os.Create(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "genicon:", err)
		os.Exit(1)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		fmt.Fprintln(os.Stderr, "genicon:", err)
		os.Exit(1)
	}
	fmt.Println("wrote", out)
}
