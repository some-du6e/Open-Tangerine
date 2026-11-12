package main

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// canvas is premultiplied BGRA, the layout UpdateLayeredWindow wants.
// Drawing outside clip is skipped, so a frame only repaints what moved.
type canvas struct {
	w, h int
	pix  []byte
	clip image.Rectangle
}

func newCanvas(w, h int, pix []byte) *canvas {
	if pix == nil {
		pix = make([]byte, w*h*4)
	}
	return &canvas{w: w, h: h, pix: pix, clip: image.Rect(0, 0, w, h)}
}

// clear sets r to black at alpha a. Alpha 1 is invisible but still catches drops.
func (c *canvas) clear(r image.Rectangle, a byte) {
	r = r.Intersect(image.Rect(0, 0, c.w, c.h))
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := c.pix[(y*c.w+r.Min.X)*4 : (y*c.w+r.Max.X)*4]
		for i := 0; i < len(row); i += 4 {
			row[i], row[i+1], row[i+2], row[i+3] = 0, 0, 0, a
		}
	}
}

// blend composites a straight-alpha color with extra coverage onto one pixel.
func (c *canvas) blend(x, y int, col color.NRGBA, coverage float64) {
	a := float64(col.A) / 255 * coverage
	if a <= 0 {
		return
	}
	i := (y*c.w + x) * 4
	keep := 1 - a
	c.pix[i] = byte(float64(col.B)*a + float64(c.pix[i])*keep)
	c.pix[i+1] = byte(float64(col.G)*a + float64(c.pix[i+1])*keep)
	c.pix[i+2] = byte(float64(col.R)*a + float64(c.pix[i+2])*keep)
	c.pix[i+3] = byte(255*a + float64(c.pix[i+3])*keep)
}

func (c *canvas) bounds(cx, cy, r float64) image.Rectangle {
	return image.Rect(int(cx-r-1), int(cy-r-1), int(cx+r+2), int(cy+r+2)).Intersect(c.clip)
}

// disc draws an antialiased circle shaded from inner (at the highlight, up and
// left of center) to outer at the rim.
func (c *canvas) disc(cx, cy, r float64, inner, outer color.NRGBA) {
	hx, hy := cx-r*0.35, cy-r*0.4
	b := c.bounds(cx, cy, r)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			cover := math.Min(1, math.Max(0, r-math.Hypot(px-cx, py-cy)+0.5))
			if cover == 0 {
				continue
			}
			t := math.Min(1, math.Hypot(px-hx, py-hy)/(r*1.5))
			c.blend(x, y, mix(inner, outer, t), cover)
		}
	}
}

func (c *canvas) ring(cx, cy, r, width float64, col color.NRGBA) {
	b := c.bounds(cx, cy, r+width)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			d := math.Abs(math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy) - r)
			if cover := math.Min(1, math.Max(0, width/2-d+0.5)); cover > 0 {
				c.blend(x, y, col, cover)
			}
		}
	}
}

func (c *canvas) pill(r image.Rectangle, col color.NRGBA) {
	rad := float64(r.Dy()) / 2
	b := r.Intersect(c.clip)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			nx := math.Max(float64(r.Min.X)+rad, math.Min(float64(r.Max.X)-rad, px))
			cover := math.Min(1, math.Max(0, rad-math.Hypot(px-nx, py-(float64(r.Min.Y)+rad))+0.5))
			c.blend(x, y, col, cover)
		}
	}
}

var faces = map[float64]font.Face{}

func face(size float64) font.Face {
	if f, ok := faces[size]; ok {
		return f
	}
	ttf, _ := opentype.Parse(gobold.TTF)
	f, _ := opentype.NewFace(ttf, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	faces[size] = f
	return f
}

// text draws s centered on (cx, cy).
func (c *canvas) text(s string, cx, cy, size float64, col color.NRGBA) {
	f := face(math.Round(size))
	d := font.Drawer{Face: f}
	w := d.MeasureString(s).Ceil()
	m := f.Metrics()
	h := (m.Ascent + m.Descent).Ceil()
	box := image.Rect(int(cx)-w/2, int(cy)-h/2, int(cx)-w/2+w, int(cy)-h/2+h)
	if !box.Overlaps(c.clip) {
		return
	}
	mask := image.NewAlpha(image.Rect(0, 0, w, h))
	d.Dst, d.Src = mask, image.Opaque
	d.Dot = fixed.P(0, m.Ascent.Ceil())
	d.DrawString(s)
	area := box.Intersect(c.clip).Intersect(image.Rect(0, 0, c.w, c.h))
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			if a := mask.AlphaAt(x-box.Min.X, y-box.Min.Y).A; a > 0 {
				c.blend(x, y, col, float64(a)/255)
			}
		}
	}
}

func mix(a, b color.NRGBA, t float64) color.NRGBA {
	l := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t) }
	return color.NRGBA{l(a.R, b.R), l(a.G, b.G), l(a.B, b.B), l(a.A, b.A)}
}

func fade(c color.NRGBA, f float64) color.NRGBA {
	c.A = uint8(float64(c.A) * f)
	return c
}

var (
	orangeLight = color.NRGBA{255, 196, 140, 255}
	orange      = color.NRGBA{235, 79, 12, 245}
	glassLight  = color.NRGBA{255, 255, 255, 230}
	glass       = color.NRGBA{183, 206, 220, 205}
	white       = color.NRGBA{255, 255, 255, 230}
	ink         = color.NRGBA{40, 64, 78, 255}
	shadow      = color.NRGBA{12, 33, 48, 45}
	mouth       = color.NRGBA{20, 28, 34, 110}
)
