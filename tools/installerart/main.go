// Command installerart draws the Windows installer's pixel art: a
// DropLink scene for the wizard background (a docking station on an alien
// ridge, the cargo drone in flight, a ringed planet) in a light and a dark
// version, and the app icon on a rounded tile for the header and welcome
// pages. Everything is drawn on a small art grid and scaled up with
// nearest-neighbour, so it stays crisp at every DPI.
//
// Run from the repo root: go run ./tools/installerart
// Output: packaging/windows/art/*.png (committed, so building the installer
// needs no Go).
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

// Art grid for the background: 298 x 216 (497:360, Inno's image area).
const (
	artW = 298
	artH = 216
)

type palette struct {
	name                                   string
	sky, sky2, star, starDim               color.NRGBA
	far, near, ground, ground2, edge       color.NRGBA
	cyan, cyanDim, magenta, amber, ink     color.NRGBA
	planet, planetShade, ring, tile, tile2 color.NRGBA
	stars                                  int // how many stars in the sky
}

func hex(s string) color.NRGBA {
	var r, g, b uint8
	fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b)
	return color.NRGBA{r, g, b, 255}
}

var light = palette{
	name: "light",
	sky:  hex("#eef9fc"), sky2: hex("#cdeef6"), star: hex("#9fd8e6"), starDim: hex("#c6e9f1"),
	far: hex("#a6d8e5"), near: hex("#6fb6c9"), ground: hex("#2f6f86"), ground2: hex("#1d4c5f"), edge: hex("#8fd0e0"),
	cyan: hex("#13b6cf"), cyanDim: hex("#5fc9da"), magenta: hex("#e23b8f"), amber: hex("#f0a020"), ink: hex("#0b2a36"),
	planet: hex("#f6c37a"), planetShade: hex("#e59a4a"), ring: hex("#e23b8f"), tile: hex("#dff4f9"), tile2: hex("#c4e9f2"),
	stars: 25,
}

var dark = palette{
	name: "dark",
	sky:  hex("#05070d"), sky2: hex("#0c1730"), star: hex("#d8f6ff"), starDim: hex("#3b5370"),
	far: hex("#14244a"), near: hex("#1c3360"), ground: hex("#0b1322"), ground2: hex("#070b14"), edge: hex("#2b4f86"),
	cyan: hex("#3ef2ff"), cyanDim: hex("#138a9e"), magenta: hex("#ff3ea5"), amber: hex("#ffb63e"), ink: hex("#d8f6ff"),
	planet: hex("#c89bff"), planetShade: hex("#6a3fa0"), ring: hex("#3ef2ff"), tile: hex("#0d1424"), tile2: hex("#1d2b48"),
	stars: 260,
}

// --- a tiny canvas -----------------------------------------------------

type canvas struct {
	w, h int
	px   []color.NRGBA
}

func newCanvas(w, h int) *canvas { return &canvas{w: w, h: h, px: make([]color.NRGBA, w*h)} }

func (c *canvas) set(x, y int, col color.NRGBA) {
	if x >= 0 && y >= 0 && x < c.w && y < c.h {
		c.px[y*c.w+x] = col
	}
}

func (c *canvas) rect(x, y, w, h int, col color.NRGBA) {
	for j := y; j < y+h; j++ {
		for i := x; i < x+w; i++ {
			c.set(i, j, col)
		}
	}
}

// scaled renders the canvas n times larger, nearest-neighbour.
func (c *canvas) scaled(n int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, c.w*n, c.h*n))
	for y := 0; y < c.h*n; y++ {
		for x := 0; x < c.w*n; x++ {
			img.SetNRGBA(x, y, c.px[(y/n)*c.w+x/n])
		}
	}
	return img
}

var bayer = [16]float64{0, 8, 2, 10, 12, 4, 14, 6, 3, 11, 1, 9, 15, 7, 13, 5}

// dither reports whether (x, y) is lit at coverage 0..1 (4x4 Bayer).
func dither(x, y int, f float64) bool { return f > (bayer[(y&3)<<2|(x&3)]+0.5)/16 }

// rng is a small seeded generator so the art is the same on every build.
type rng uint32

func (r *rng) next() float64 {
	*r = *r*1664525 + 1013904223
	return float64(*r>>8) / float64(1<<24)
}

// sprite draws rows of characters with a color map; "." is transparent.
func (c *canvas) sprite(x, y int, rows []string, cols map[byte]color.NRGBA) {
	for j, row := range rows {
		for i := 0; i < len(row); i++ {
			if col, ok := cols[row[i]]; ok {
				c.set(x+i, y+j, col)
			}
		}
	}
}

// The app icon (same art as tools/genicon) and the cargo drone (as in
// web/js/pixel/sprites.js).
var icon = []string{
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

var drone = []string{
	"....ccccc....",
	"..ccCwwwCcc..",
	".ccCCCCCCCcc.",
	"cCCdddddddCCc",
	".C.e.....e.C.",
	".C.........C.",
	".a.........a.",
	".A.........A.",
}

var cube = []string{"acca", "cwcc", "cccC", "aCCa"}

func iconColors() map[byte]color.NRGBA {
	return map[byte]color.NRGBA{
		'k': hex("#0b0f1a"), 'c': hex("#3ef2ff"), 'C': hex("#136e8a"),
		'm': hex("#ff3ea5"), 'w': hex("#e8fbff"), 'a': hex("#ffb63e"),
	}
}

// --- the background scene ---------------------------------------------

// ridge returns the height of a mountain line at x: layered sines, seeded.
func ridge(x int, base, amp float64, seed float64) float64 {
	fx := float64(x)
	return base - amp*(0.55*math.Sin(fx/23+seed)+0.3*math.Sin(fx/9.7+seed*2.1)+0.15*math.Sin(fx/4.3+seed*3.7))
}

func background(p palette) *canvas {
	c := newCanvas(artW, artH)
	r := rng(0xd201)

	// Sky: flat where the text sits, dithering into a horizon glow below.
	for y := 0; y < artH; y++ {
		f := math.Max(0, (float64(y)-110)/70)
		for x := 0; x < artW; x++ {
			if dither(x, y, f) {
				c.set(x, y, p.sky2)
			} else {
				c.set(x, y, p.sky)
			}
		}
	}
	// Stars (dark) or high specks of cloud (light); thinner under the text.
	for i := 0; i < p.stars; i++ {
		x, y := int(r.next()*artW), int(r.next()*170)
		col := p.starDim
		if r.next() < 0.25 {
			col = p.star
		}
		c.set(x, y, col)
		if p.name == "dark" && r.next() < 0.06 && y > 60 {
			c.set(x-1, y, p.starDim)
			c.set(x+1, y, p.starDim)
			c.set(x, y-1, p.starDim)
			c.set(x, y+1, p.starDim)
		}
	}

	// A ringed planet low over the ridge, dithered shading.
	px, py, pr := 232.0, 150.0, 15.0
	for y := int(py - pr - 1); y <= int(py+pr+1); y++ {
		for x := int(px - pr - 1); x <= int(px+pr+1); x++ {
			d := math.Hypot(float64(x)-px, float64(y)-py)
			if d <= pr {
				shade := (float64(x)-px+pr*0.4)/(pr*1.4) + (float64(y)-py)/(pr*3)
				if dither(x, y, shade) {
					c.set(x, y, p.planetShade)
				} else {
					c.set(x, y, p.planet)
				}
			}
		}
	}
	for t := 0.0; t < 2*math.Pi; t += 0.004 {
		x := px + math.Cos(t)*pr*1.9
		y := py + math.Sin(t)*pr*0.45 - math.Cos(t)*3
		if math.Sin(t) < 0 && math.Hypot(x-px, y-py) < pr {
			continue // behind the planet
		}
		c.set(int(math.Round(x)), int(math.Round(y)), p.ring)
	}

	// Far and near ridges.
	for x := 0; x < artW; x++ {
		far := int(ridge(x, 178, 10, 1.3))
		near := int(ridge(x, 193, 7, 4.1))
		for y := far; y < artH; y++ {
			col := p.far
			if y == far {
				col = p.edge
			}
			c.set(x, y, col)
		}
		for y := near; y < artH; y++ {
			// Lit slopes facing the planet, shaded the other way.
			slope := ridge(x+1, 193, 7, 4.1) - ridge(x-1, 193, 7, 4.1)
			col := p.near
			if slope > 0.4 && dither(x, y, 0.5) {
				col = p.far
			}
			c.set(x, y, col)
		}
	}

	// Ground with a perspective grid.
	for y := 202; y < artH; y++ {
		for x := 0; x < artW; x++ {
			col := p.ground
			if dither(x, y, float64(y-202)/14) {
				col = p.ground2
			}
			c.set(x, y, col)
		}
	}
	for x := -200; x < artW+200; x += 14 {
		for y := 202; y < artH; y++ {
			gx := 149 + int(float64(x-149)*(1+float64(y-202)*0.18))
			if gx >= 0 && gx < artW && (y%2 == 0) {
				c.set(gx, y, p.cyanDim)
			}
		}
	}
	c.rect(0, 202, artW, 1, p.cyanDim)

	// Docking tower on the left, with blinking lights.
	tx := 34
	c.rect(tx, 150, 4, 52, p.ground2)
	c.rect(tx-6, 150, 16, 3, p.ground2)
	c.rect(tx-3, 165, 10, 2, p.ground2)
	c.set(tx+1, 147, p.magenta)
	c.set(tx+2, 147, p.magenta)
	c.set(tx-6, 149, p.amber)
	c.set(tx+9, 149, p.amber)
	for y := 156; y < 200; y += 6 {
		c.set(tx+1, y, p.cyan)
	}
	// Radar dish next to it.
	dx, dy := 60, 188
	for i := -6; i <= 6; i++ {
		h := int(math.Sqrt(math.Max(0, 36-float64(i*i))) / 2)
		c.rect(dx+i, dy-h, 1, h+1, p.ground2)
	}
	c.rect(dx, dy, 1, 14, p.ground2)
	c.set(dx+4, dy-8, p.cyan)
	c.set(dx+5, dy-9, p.cyan)
	c.set(dx+6, dy-10, p.cyan)

	// Landing pad on the right, lights on.
	lx := 196
	c.rect(lx-14, 199, 28, 3, p.ground2)
	c.rect(lx-12, 198, 24, 1, p.cyanDim)
	for i := -12; i <= 12; i += 6 {
		c.set(lx+i, 197, p.amber)
	}

	// The data link: a dashed beam from the tower to the pad.
	for x := tx + 12; x < lx-14; x += 5 {
		y := 152 + (x-tx)*(190-152)/(lx-tx)
		c.set(x, y, p.cyan)
		c.set(x+1, y, p.cyan)
	}

	// The cargo drone in flight, towing a data cube, thrusters lit.
	cols := map[byte]color.NRGBA{'c': p.cyan, 'C': p.cyanDim, 'w': hex("#e8fbff"), 'd': p.ground2, 'e': p.edge, 'a': p.amber, 'A': p.magenta}
	c.sprite(128, 160, drone, cols)
	c.sprite(132, 170, cube, map[byte]color.NRGBA{'a': p.amber, 'c': p.cyan, 'w': hex("#e8fbff"), 'C': p.cyanDim})
	// A second, distant ship crossing the sky.
	c.set(92, 120, p.ink)
	c.set(93, 120, p.ink)
	c.set(94, 120, p.magenta)
	return c
}

// tile draws the app icon on a rounded tile, like the app's own icon.
func tile(p palette, size, scale int, transparentOutside bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	r := size / 6
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			// Rounded square with a soft second tone at the bottom.
			cx := math.Max(0, math.Max(float64(r-x), float64(x-(size-1-r))))
			cy := math.Max(0, math.Max(float64(r-y), float64(y-(size-1-r))))
			if math.Hypot(cx, cy) > float64(r) {
				if !transparentOutside {
					img.SetNRGBA(x, y, p.sky)
				}
				continue
			}
			col := p.tile
			if y > size*2/3 && dither(x/2, y/2, float64(y-size*2/3)/float64(size/3)) {
				col = p.tile2
			}
			img.SetNRGBA(x, y, col)
		}
	}
	art := len(icon) * scale
	off := (size - art) / 2
	cols := iconColors()
	for j, row := range icon {
		for i := 0; i < len(row); i++ {
			col, ok := cols[row[i]]
			if !ok {
				continue
			}
			for yy := 0; yy < scale; yy++ {
				for xx := 0; xx < scale; xx++ {
					img.SetNRGBA(off+i*scale+xx, off+j*scale+yy, col)
				}
			}
		}
	}
	return img
}

// welcome is the tall image on the Welcome / Finished pages: the icon
// tile at the top of a transparent strip, like the LatchVerify wizard.
func welcome(p palette) *image.NRGBA {
	const w, h = 404, 772 // 2x the 202 x 386 area
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	t := tile(p, 260, 14, true)
	ox, oy := (w-260)/2, 60
	for y := 0; y < 260; y++ {
		for x := 0; x < 260; x++ {
			if c := t.NRGBAAt(x, y); c.A != 0 {
				img.SetNRGBA(ox+x, oy+y, c)
			}
		}
	}
	return img
}

func save(dir, name string, img image.Image) {
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		fail(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		fail(err)
	}
	fmt.Println("wrote", filepath.Join(dir, name))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "installerart:", err)
	os.Exit(1)
}

func main() {
	dir := filepath.Join("packaging", "windows", "art")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(err)
	}
	for _, p := range []palette{light, dark} {
		bg := background(p)
		save(dir, "back-"+p.name+".png", bg.scaled(2))    // 596 x 432
		save(dir, "back-"+p.name+"@2x.png", bg.scaled(4)) // 1192 x 864
		save(dir, "small-"+p.name+".png", tile(p, 116, 6, false))
		save(dir, "small-"+p.name+"@2x.png", tile(p, 174, 9, false))
		save(dir, "welcome-"+p.name+".png", welcome(p))
	}
}
