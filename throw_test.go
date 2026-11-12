package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func aiming1(t *testing.T) *thrower {
	th := &thrower{}
	th.arm(screen, 0)
	if !th.enter([]string{`C:\pics\cat.png`}, []string{"JPG", "WEBP", "PDF"}, 960, 600, 0) {
		t.Fatal("convertible drag refused")
	}
	return th
}

func swing(th *thrower, toward point, now float64) {
	d := math.Hypot(toward.x-960, toward.y-600)
	ux, uy := (toward.x-960)/d, (toward.y-600)/d
	for i := 0; i < 5; i++ {
		k := float64(4 - i)
		th.over(960-ux*40*k, 600-uy*40*k, now-0.064+float64(i)*0.016)
	}
}

func finish(th *thrower, now float64) *job {
	for i := 0; i < 1000 && th.state != idle; i++ {
		now += 0.016
		if j := th.tick(0.016, now); j != nil {
			return j
		}
	}
	return nil
}

func TestThrowIntoBinConverts(t *testing.T) {
	th := aiming1(t)
	swing(th, th.bins[1], 1)
	th.drop(960, 600, 1)
	if th.state != inFlight {
		t.Fatalf("state %d", th.state)
	}
	j := finish(th, 1)
	if j == nil || j.target != "WEBP" || j.paths[0] != `C:\pics\cat.png` {
		t.Fatalf("got %+v", j)
	}
}

func TestGentleDropOnNothingDoesNothing(t *testing.T) {
	th := aiming1(t)
	th.drop(960, 600, 5) // no motion samples
	if th.state != fizzling || finish(th, 5) != nil || th.state != idle {
		t.Fatal("a gentle drop on empty space must not convert")
	}
}

func TestDropStraightOnBin(t *testing.T) {
	th := aiming1(t)
	b := th.bins[2]
	th.drop(b.x, b.y, 5)
	if j := finish(th, 5); j == nil || j.target != "PDF" {
		t.Fatalf("got %+v", j)
	}
}

func TestLayerStepsAside(t *testing.T) {
	th := &thrower{}
	th.arm(screen, 0)
	th.watch(0, 0.6)
	if th.state != idle {
		t.Fatal("armed layer should give up when no file drag arrives")
	}
	th = aiming1(t)
	th.watch(10, 1)
	th.watch(10, 1.7)
	if th.state != idle {
		t.Fatal("slowing down should let ordinary drops through")
	}
	th = &thrower{}
	th.arm(screen, 0)
	if th.enter([]string{"a.txt"}, nil, 0, 0, 0) {
		t.Fatal("unconvertible drag accepted")
	}
}

func TestRenderOnlyRepaintsWhatMoved(t *testing.T) {
	th := aiming1(t)
	c := newCanvas(1920, 1040, nil)
	if d := th.render(c, 1, 0); d != image.Rect(0, 0, 1920, 1040) {
		t.Fatalf("first frame should be full, got %v", d)
	}
	if a := c.pix[3]; a != 1 {
		t.Fatalf("catch layer must be hit-testable (alpha 1), got %d", a)
	}
	swing(th, th.bins[1], 1)
	th.drop(960, 600, 1)
	th.render(c, 1, 1)
	th.tick(0.016, 1.016)
	if d := th.render(c, 1, 1.016); d.Dx() > 300 || d.Dy() > 300 {
		t.Fatalf("a flight frame repainted %v", d)
	}
	if c.pix[3] != 0 {
		t.Fatal("during flight the layer must let clicks through")
	}
}

// TestPreview renders the overlay to PNGs in $PREVIEW_DIR for a look at it.
func TestPreview(t *testing.T) {
	dir := os.Getenv("PREVIEW_DIR")
	if dir == "" {
		t.Skip("set PREVIEW_DIR to render previews")
	}
	save := func(name string, c *canvas) {
		img := image.NewRGBA(image.Rect(0, 0, c.w, c.h))
		for i := 0; i < len(c.pix); i += 4 {
			// Over a desktop-ish backdrop, un-premultiplied by compositing.
			a := float64(c.pix[i+3]) / 255
			bg := color.RGBA{38, 70, 110, 255}
			img.Pix[i] = byte(float64(c.pix[i+2]) + float64(bg.R)*(1-a))
			img.Pix[i+1] = byte(float64(c.pix[i+1]) + float64(bg.G)*(1-a))
			img.Pix[i+2] = byte(float64(c.pix[i]) + float64(bg.B)*(1-a))
			img.Pix[i+3] = 255
		}
		f, _ := os.Create(filepath.Join(dir, name))
		defer f.Close()
		png.Encode(f, img)
	}
	th := &thrower{}
	th.arm(screen, 0)
	th.enter([]string{"cat.png"}, []string{"JPG", "WEBP", "PDF"}, 960, 600, 0)
	th.hover = 1
	c := newCanvas(1920, 1040, nil)
	th.render(c, 1, 0)
	save("aiming.png", c)
	th.hover = -1
	swing(th, th.bins[1], 1)
	th.drop(960, 600, 1)
	now := 1.0
	for i := 0; i < 9; i++ {
		now += 0.016
		th.tick(0.016, now)
	}
	th.full = true
	th.render(c, 1, now)
	save("flight.png", c)
}
