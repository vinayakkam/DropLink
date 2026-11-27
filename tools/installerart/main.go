// Command installerart draws the Windows installer's pixel art: a
// DropLink scene for the wizard background (a ringed planet rising over
// crystal-capped ridges and a sea, a domed colony with a rocket lifting
// off, courier drones and the cargo drone) in a light and a dark version,
// and the app icon on a rounded tile for the header and welcome pages.
// Everything is drawn on a small art grid and scaled up with
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

// horizon is the sea line on the art grid. The sky above y=120 stays calm
// so the wizard's text is easy to read.
const horizon = 184

type palette struct {
	name                                 string
	sky, skyMid, skyLow, star, starDim   color.NRGBA // sky: the top, under the text
	nebula, cloud                        color.NRGBA
	planet, planetShade, glow, ring      color.NRGBA
	farMt, farLit, midMt, midLit, cap    color.NRGBA
	nearMt, nearLit, water, water2, glim color.NRGBA
	ground, ground2, smoke               color.NRGBA
	cyan, cyanDim, magenta, amber, ink   color.NRGBA
	tile, tile2                          color.NRGBA
	stars                                int // how many stars in the sky
}

func hex(s string) color.NRGBA {
	var r, g, b uint8
	fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b)
	return color.NRGBA{r, g, b, 255}
}

// Light: dawn on DropLink's world, a coral planet rising in a pale sky.
var light = palette{
	name: "light",
	sky:  hex("#f4fbfd"), skyMid: hex("#e4f4f8"), skyLow: hex("#fde6d8"), star: hex("#ffffff"), starDim: hex("#eaf6f9"),
	nebula: hex("#fbeee6"), cloud: hex("#ffffff"),
	planet: hex("#ff9a62"), planetShade: hex("#ee5f52"), glow: hex("#ffd2b8"), ring: hex("#e23b8f"),
	farMt: hex("#c6dee6"), farLit: hex("#dcebef"), midMt: hex("#8db8c8"), midLit: hex("#b2d3de"), cap: hex("#ffffff"),
	nearMt: hex("#4d8399"), nearLit: hex("#6ca2b5"), water: hex("#b5dde7"), water2: hex("#92c8d6"), glim: hex("#ffffff"),
	ground: hex("#1f4f63"), ground2: hex("#133949"), smoke: hex("#ffffff"),
	cyan: hex("#13b6cf"), cyanDim: hex("#5fc9da"), magenta: hex("#e23b8f"), amber: hex("#f0a020"), ink: hex("#2a5466"),
	tile: hex("#ffffff"), tile2: hex("#d3eef5"),
}

// Dark: night, a violet planet with a cyan ring over a dense starfield.
var dark = palette{
	name: "dark",
	sky:  hex("#04060c"), skyMid: hex("#070c1b"), skyLow: hex("#111c3c"), star: hex("#e6f8ff"), starDim: hex("#4d5f80"),
	nebula: hex("#0d1330"), cloud: hex("#1b2850"),
	planet: hex("#b991ff"), planetShade: hex("#5b3a9a"), glow: hex("#221c52"), ring: hex("#3ef2ff"),
	farMt: hex("#17224a"), farLit: hex("#22336a"), midMt: hex("#0f1734"), midLit: hex("#1f3264"), cap: hex("#8fb3e6"),
	nearMt: hex("#090e20"), nearLit: hex("#131d3e"), water: hex("#0b1431"), water2: hex("#070d22"), glim: hex("#9fe9ff"),
	ground: hex("#05080f"), ground2: hex("#020309"), smoke: hex("#33406a"),
	cyan: hex("#3ef2ff"), cyanDim: hex("#138a9e"), magenta: hex("#ff3ea5"), amber: hex("#ffb63e"), ink: hex("#7d8fb8"),
	tile: hex("#111a2e"), tile2: hex("#070b16"),
	stars: 900,
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

func (c *canvas) at(x, y int) color.NRGBA {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return color.NRGBA{}
	}
	return c.px[y*c.w+x]
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

// hash is fixed noise in 0..1 for a column.
func hash(x int, seed uint32) float64 {
	h := uint32(x)*2654435761 ^ seed*2246822519
	h ^= h >> 15
	h *= 2246822519
	h ^= h >> 13
	return float64(h&0xffff) / 0xffff
}

func mix(a, b color.NRGBA, t float64) color.NRGBA {
	t = math.Max(0, math.Min(1, t))
	l := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t + 0.5) }
	return color.NRGBA{l(a.R, b.R), l(a.G, b.G), l(a.B, b.B), 255}
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

var rocket = []string{".w.", "www", "wcw", "www", "wcw", "w.w", "a.a", ".A."}

func iconColors() map[byte]color.NRGBA {
	return map[byte]color.NRGBA{
		'k': hex("#0b0f1a"), 'c': hex("#3ef2ff"), 'C': hex("#136e8a"),
		'm': hex("#ff3ea5"), 'w': hex("#e8fbff"), 'a': hex("#ffb63e"),
	}
}

// --- the background scene ---------------------------------------------

type peak struct{ x, h, slope float64 }

// mountains returns, per column, the top of a range of peaks (jagged by
// noise) and whether that column's slope faces the planet on the right.
func mountains(peaks []peak, jag float64, seed uint32) (top []int, lit []bool) {
	top, lit = make([]int, artW), make([]bool, artW)
	for x := 0; x < artW; x++ {
		best, side := 0.0, false
		for _, p := range peaks {
			if h := p.h - math.Abs(float64(x)-p.x)*p.slope; h > best {
				best, side = h, float64(x) > p.x
			}
		}
		if best > 0 {
			best += (hash(x, seed) - 0.5) * jag
		}
		top[x], lit[x] = horizon-int(best), side
	}
	return top, lit
}

func background(p palette) *canvas {
	c := newCanvas(artW, artH)
	r := rng(0xd201)
	sky(c, p, &r)
	planet(c, p)
	ranges(c, p)
	sea(c, p, &r)
	colony(c, p)
	flyers(c, p)
	return c
}

// sky: flat under the text, dithered bands down to a glow over the
// horizon, a faint nebula or haze, stars and thin clouds.
func sky(c *canvas, p palette, r *rng) {
	for y := 0; y < horizon; y++ {
		for x := 0; x < artW; x++ {
			col := p.sky
			if t := (float64(y) - 70) / 60; t > 0 && dither(x, y, math.Min(t, 1)) {
				col = p.skyMid
			}
			if t := (float64(y) - 125) / 55; t > 0 && dither(x, y, math.Min(t, 1)) {
				col = p.skyLow
			}
			c.set(x, y, col)
		}
	}
	for x := 0; x < artW; x++ {
		mid := 112 + 14*math.Sin(float64(x)/41+0.7)
		for y := int(mid) - 10; y < int(mid)+10; y++ {
			if dither(x, y, 0.35*(1-math.Abs(float64(y)-mid)/10)) {
				c.set(x, y, p.nebula)
			}
		}
	}
	for i := 0; i < p.stars; i++ {
		x, y := int(r.next()*artW), int(r.next()*(horizon-20))
		if y < 70 && r.next() < 0.55 {
			continue // fewer behind the text
		}
		col := p.starDim
		if r.next() < 0.18 {
			col = p.star
		}
		c.set(x, y, col)
		if r.next() < 0.012 && y > 40 {
			for _, d := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
				c.set(x+d[0], y+d[1], p.starDim)
			}
		}
	}
	if p.stars > 0 { // a shooting star
		for i := 0; i < 16; i++ {
			col := p.starDim
			if i > 11 {
				col = p.star
			}
			c.rect(212+i*2, 58+i, 2, 1, col)
		}
	}
	for i := 0; i < 9; i++ {
		x, y := int(r.next()*artW), 140+int(r.next()*36)
		w := 8 + int(r.next()*26)
		c.rect(x, y, w, 1, p.cloud)
		c.rect(x+3, y-1, w/2, 1, p.cloud)
	}
}

// planet rises in the valley: a dithered glow, the striped lower half of
// a retro sunrise and a tilted ring.
func planet(c *canvas, p palette) {
	px, py, pr := 208.0, 164.0, 19.0
	for y := int(py - pr*2); y <= int(py+pr*2); y++ {
		for x := int(px - pr*2); x <= int(px+pr*2); x++ {
			d := math.Hypot(float64(x)-px, float64(y)-py)
			if d > pr && d < pr*2 && dither(x, y, 0.55*(1-(d-pr)/pr)) {
				c.set(x, y, p.glow)
			}
		}
	}
	for y := int(py - pr); y <= int(py+pr); y++ {
		dy := float64(y) - py
		if dy > 3 && int(dy)%6 < 1+int(dy/7) {
			continue // stripes widen toward the horizon
		}
		for x := int(px - pr); x <= int(px+pr); x++ {
			if math.Hypot(float64(x)-px, dy) > pr {
				continue
			}
			if dither(x, y, (float64(x)-px+pr*0.3)/(pr*1.6)+dy/(pr*2.5)) {
				c.set(x, y, p.planetShade)
			} else {
				c.set(x, y, p.planet)
			}
		}
	}
	for t := 0.0; t < 2*math.Pi; t += 0.003 {
		x := px + math.Cos(t)*pr*1.85
		y := py - 2 + math.Sin(t)*pr*0.38 - math.Cos(t)*4
		if math.Sin(t) < 0 && math.Hypot(x-px, y-py) < pr {
			continue // behind the planet
		}
		c.set(int(math.Round(x)), int(math.Round(y)), p.ring)
	}
}

// ranges: hazy far peaks, crystal-capped middle peaks and dark near
// hills, leaving the valley open for the planet, and a shore of crystal
// shards like a tree line.
func ranges(c *canvas, p palette) {
	layer := func(top []int, lit []bool, col, litCol color.NRGBA, capDepth, capAbove int) {
		for x := 0; x < artW; x++ {
			for y := max(top[x], 0); y < horizon; y++ {
				cc := col
				if lit[x] && dither(x, y, 0.6) {
					cc = litCol
				}
				if capDepth > 0 && top[x] < capAbove && y-top[x] < capDepth+int(hash(x, 7)*3) {
					cc = p.cap
					if !lit[x] && dither(x, y, 0.5) {
						cc = litCol
					}
				}
				c.set(x, y, cc)
			}
		}
	}
	top, lit := mountains([]peak{{18, 26, 0.9}, {64, 34, 1.0}, {118, 24, 0.8}, {160, 30, 1.1}, {256, 36, 0.95}, {292, 26, 0.9}}, 3, 11)
	layer(top, lit, p.farMt, p.farLit, 3, 162)
	top, lit = mountains([]peak{{40, 30, 1.25}, {92, 22, 1.1}, {138, 17, 1.3}, {244, 26, 1.2}, {284, 31, 1.3}}, 4, 23)
	layer(top, lit, p.midMt, p.midLit, 4, 164)
	top, lit = mountains([]peak{{10, 18, 0.7}, {70, 12, 0.55}, {276, 16, 0.6}}, 2, 31)
	layer(top, lit, p.nearMt, p.nearLit, 0, 0)

	for x := 0; x < artW; x++ {
		if x > 112 && x < 252 || hash(x, 41) < 0.45 {
			continue
		}
		h := 2 + int(hash(x, 43)*6)
		for k := 0; k < h; k++ {
			c.set(x, horizon-1-k, p.ground)
			if k < h/3 {
				c.set(x-1, horizon-1-k, p.ground)
				c.set(x+1, horizon-1-k, p.ground)
			}
		}
		if hash(x, 47) < 0.1 {
			c.set(x, horizon-1-h, p.cyan)
		}
	}
}

// sea reflects the planet and the hills in broken, wavy lines, with
// glints on the surface.
func sea(c *canvas, p palette, r *rng) {
	for y := horizon; y < artH; y++ {
		dy := y - horizon
		for x := 0; x < artW; x++ {
			col := p.water
			if dither(x, y, float64(dy)/float64(artH-horizon)) {
				col = p.water2
			}
			c.set(x, y, col)
		}
		if dy%3 == 2 {
			continue // gaps between reflection lines
		}
		shift := int(2 * math.Sin(float64(y)*1.7))
		for x := 0; x < artW; x++ {
			switch src := c.at(x+shift, horizon-1-dy*2/3); src {
			case p.planet, p.planetShade, p.ring, p.cap:
				c.set(x, y, mix(src, p.water, 0.35+float64(dy)/60))
			case p.nearMt, p.midMt, p.ground:
				c.set(x, y, mix(src, p.water, 0.55))
			}
		}
	}
	for i := 0; i < 34; i++ {
		x, y := int(r.next()*artW), horizon+2+int(r.next()*28)
		c.rect(x, y, 2+int(r.next()*5), 1, p.glim)
	}
}

// colony: the left shore with domes and lit windows, a beacon tower and
// a rocket lifting off on a curl of smoke; rocks on the right shore and a
// hover boat with its wake.
func colony(c *canvas, p palette) {
	for x := 0; x < 112; x++ {
		top := 196 + int(3*math.Sin(float64(x)/9)) + int(hash(x, 53)*2) + max(0, (x-78)*(x-78)/40)
		for y := top; y < artH; y++ {
			col := p.ground
			if dither(x, y, float64(y-top)/16) {
				col = p.ground2
			}
			c.set(x, y, col)
		}
	}
	dome := func(cx, cy, rad int) {
		for y := cy - rad; y <= cy; y++ {
			for x := cx - rad; x <= cx+rad; x++ {
				if (x-cx)*(x-cx)+(y-cy)*(y-cy) <= rad*rad {
					c.set(x, y, p.ground)
				}
			}
		}
		for i := -rad + 2; i < rad-1; i += 3 {
			c.set(cx+i, cy-1, p.amber)
		}
		c.set(cx, cy-rad+1, p.cyan)
	}
	dome(30, 197, 8)
	dome(46, 198, 5)
	dome(16, 199, 4)

	tx := 62
	c.rect(tx, 168, 2, 30, p.ground)
	c.rect(tx-3, 172, 8, 1, p.ground)
	c.rect(tx-2, 180, 6, 1, p.ground)
	c.rect(tx, 166, 2, 1, p.magenta)
	c.set(tx-3, 171, p.amber)
	c.set(tx+4, 171, p.amber)

	rx := 84
	c.rect(rx-6, 196, 13, 2, p.ground)
	c.set(rx-6, 195, p.amber)
	c.set(rx+6, 195, p.amber)
	for i := 0; i < 26; i++ {
		y := 194 - i*2
		x := rx + int(3*math.Sin(float64(i)/3.5))
		rad := 1 + i/7
		for yy := -rad; yy <= rad; yy++ {
			for xx := -rad; xx <= rad; xx++ {
				if xx*xx+yy*yy <= rad*rad && dither(x+xx, y+yy, 0.85-float64(i)/40) {
					c.set(x+xx, y+yy, p.smoke)
				}
			}
		}
	}
	c.sprite(rx+int(3*math.Sin(26/3.5))-1, 132, rocket,
		map[byte]color.NRGBA{'w': p.ink, 'c': p.cyan, 'a': p.amber, 'A': p.magenta})

	for x := 240; x < artW; x++ {
		top := 199 - int(4*math.Sin(float64(x-258)/12)) + int(hash(x, 59)*2) + max(0, (268-x)*(268-x)/30)
		for y := top; y < artH; y++ {
			c.set(x, y, p.ground)
		}
	}
	c.set(271, 193, p.amber)

	bx, by := 150, 199
	c.rect(bx, by, 9, 2, p.ground)
	c.rect(bx+2, by-2, 4, 2, p.ground)
	c.set(bx+3, by-3, p.cyan)
	for i := 1; i < 6; i++ {
		c.rect(bx-3*i-4, by+1+i%2, 3, 1, p.glim)
	}
}

// flyers: a flock of courier drones and the cargo drone towing a cube.
func flyers(c *canvas, p palette) {
	for _, d := range [][2]int{{96, 128}, {104, 124}, {101, 133}, {112, 130}, {89, 136}} {
		c.set(d[0]-1, d[1]-1, p.ink)
		c.set(d[0], d[1], p.ink)
		c.set(d[0]+1, d[1]-1, p.ink)
	}
	cols := map[byte]color.NRGBA{'c': p.cyan, 'C': p.cyanDim, 'w': hex("#e8fbff"), 'd': p.ground, 'e': p.cyanDim, 'a': p.amber, 'A': p.magenta}
	c.sprite(150, 128, drone, cols)
	c.sprite(154, 138, cube, map[byte]color.NRGBA{'a': p.amber, 'c': p.cyan, 'w': hex("#e8fbff"), 'C': p.cyanDim})
}

// --- icon tiles ---------------------------------------------------------

// tile draws the app icon on a rounded tile with a soft vertical gradient,
// like the app's own icon.
func tile(p palette, size, scale int, transparentOutside bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	r := size / 6
	block := max(scale/2, 1)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			cx := math.Max(0, math.Max(float64(r-x), float64(x-(size-1-r))))
			cy := math.Max(0, math.Max(float64(r-y), float64(y-(size-1-r))))
			if math.Hypot(cx, cy) > float64(r) {
				if !transparentOutside {
					img.SetNRGBA(x, y, p.sky)
				}
				continue
			}
			col := p.tile
			if dither(x/block, y/block, float64(y)/float64(size)) {
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
// tile at the top of a transparent strip, over the background scene.
func welcome(p palette) *image.NRGBA {
	const w, h = 404, 772 // 2x the 202 x 386 area
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	const size = 224
	t := tile(p, size, 12, true)
	ox, oy := (w-size)/2, 40
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
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
