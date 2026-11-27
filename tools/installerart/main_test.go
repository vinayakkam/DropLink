package main

import "testing"

func TestArtSizesAndPalettes(t *testing.T) {
	for _, p := range []palette{light, dark} {
		bg := background(p)
		if bg.w != artW || bg.h != artH {
			t.Fatalf("background %dx%d", bg.w, bg.h)
		}
		if img := bg.scaled(2); img.Bounds().Dx() != 596 || img.Bounds().Dy() != 432 {
			t.Fatalf("1x background %v", img.Bounds())
		}
		if s := tile(p, 116, 6, false); s.Bounds().Dx() != 116 {
			t.Fatalf("small image %v", s.Bounds())
		}
		w := welcome(p)
		if w.Bounds().Dx() != 404 || w.Bounds().Dy() != 772 {
			t.Fatalf("welcome %v", w.Bounds())
		}
		// The welcome image is transparent around the tile.
		if w.NRGBAAt(0, 0).A != 0 || w.NRGBAAt(202, 190).A == 0 {
			t.Fatal("welcome transparency")
		}
	}
	// Light and dark really differ, and the sky stays clear under the text.
	l, d := background(light), background(dark)
	if l.px[0] == d.px[0] {
		t.Fatal("light and dark backgrounds start alike")
	}
	if l.px[10*artW+10] != light.sky && l.px[10*artW+10] != light.starDim && l.px[10*artW+10] != light.star {
		t.Fatal("unexpected color under the text area")
	}
}

func TestArtIsDeterministic(t *testing.T) {
	a, b := background(dark), background(dark)
	for i := range a.px {
		if a.px[i] != b.px[i] {
			t.Fatal("art differs between runs")
		}
	}
}
