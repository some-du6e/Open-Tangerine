package main

import (
	"image"
	"math"
	"path/filepath"
	"strconv"
	"strings"
)

type state int

const (
	idle     state = iota
	armed          // swinging fast; invisible layer waits for the drag to reach it
	aiming         // a convertible file is over the layer; bins are showing
	inFlight       // let go mid-swing
	landing        // shrinking into a bin
	fizzling       // missed, or set down gently on nothing
)

const (
	landTime   = 0.32
	fadeTime   = 0.45
	armTimeout = 0.5 // no file drag reached the layer: it was a window move or rubber band
	slowSpeed  = 220.0
	slowDisarm = 0.6 // dawdling without hovering a bin: step aside for ordinary drops
)

type job struct {
	paths  []string
	target string
}

// thrower is the catch layer's state for one screen, in local logical pixels.
type thrower struct {
	state     state
	since     float64
	area      rect
	paths     []string
	formats   []string
	bins      []point
	hover     int
	target    int
	f         *flight
	samples   []sample
	slowSince float64
	full      bool // next render repaints everything
	lastBox   image.Rectangle
}

func (t *thrower) set(s state, now float64) {
	t.state, t.since, t.full = s, now, true
}

func (t *thrower) catching() bool { return t.state == armed || t.state == aiming }

func (t *thrower) sample(x, y, now float64) {
	t.samples = append(t.samples, sample{now, x, y})
	if len(t.samples) > 32 {
		t.samples = t.samples[1:]
	}
}

func (t *thrower) arm(area rect, now float64) {
	*t = thrower{area: area, hover: -1, target: -1, samples: t.samples}
	t.set(armed, now)
}

func (t *thrower) disarm(now float64) {
	if t.catching() {
		t.set(idle, now)
	}
}

// enter is a file drag arriving over the layer. It reports whether to accept it.
func (t *thrower) enter(paths, formats []string, x, y, now float64) bool {
	if !t.catching() || len(formats) == 0 {
		return false
	}
	t.paths, t.formats = paths, formats
	t.bins = layoutBins(len(formats), t.area)
	t.hover = binAt(t.bins, x, y)
	t.set(aiming, now)
	return true
}

func (t *thrower) over(x, y, now float64) {
	t.sample(x, y, now)
	if h := binAt(t.bins, x, y); h != t.hover {
		t.hover, t.full = h, true
	}
}

// watch runs every poll while the button is held, to step aside when the user slows down.
func (t *thrower) watch(speed, now float64) {
	switch {
	case t.state == armed && now-t.since > armTimeout:
		t.disarm(now)
	case t.state == aiming && speed < slowSpeed && t.hover < 0:
		if t.slowSince == 0 {
			t.slowSince = now
		} else if now-t.slowSince > slowDisarm {
			t.disarm(now)
		}
	default:
		t.slowSince = 0
	}
}

func (t *thrower) drop(x, y, now float64) {
	if t.state != aiming {
		return
	}
	vx, vy := velocity(t.samples, now)
	t.f = &flight{x: x, y: y, vx: vx, vy: vy}
	switch {
	case binAt(t.bins, x, y) >= 0:
		t.target = binAt(t.bins, x, y)
		t.set(landing, now)
	case math.Hypot(vx, vy) >= throwSpeed:
		t.set(inFlight, now)
	default:
		t.set(fizzling, now)
	}
}

// tick advances the animation, returning a job once a file lands.
func (t *thrower) tick(dt, now float64) *job {
	switch t.state {
	case inFlight:
		switch r := t.f.step(dt, t.area, t.bins); r {
		case flying:
		case missed:
			t.set(fizzling, now)
		default:
			t.target = r
			t.set(landing, now)
		}
	case landing:
		if now-t.since >= landTime {
			j := &job{t.paths, t.formats[t.target]}
			t.set(idle, now)
			return j
		}
	case fizzling:
		if now-t.since >= fadeTime {
			t.set(idle, now)
		}
	}
	return nil
}

func (t *thrower) ballLabel() string {
	if len(t.paths) > 1 {
		return strconv.Itoa(len(t.paths))
	}
	ext := strings.ToUpper(strings.TrimPrefix(filepath.Ext(t.paths[0]), "."))
	if len(ext) > 4 || ext == "" {
		return "FILE"
	}
	return ext
}

// ballBox is where the ball and its trail are drawn, in physical pixels.
func (t *thrower) ballBox(s float64) image.Rectangle {
	if t.f == nil || t.state == idle {
		return image.Rectangle{}
	}
	pad := (ballRadius + 4) * s
	box := image.Rect(int(t.f.x*s-pad), int(t.f.y*s-pad), int(t.f.x*s+pad), int(t.f.y*s+pad))
	for _, p := range t.f.trail {
		box = box.Union(image.Rect(int(p.x*s-pad), int(p.y*s-pad), int(p.x*s+pad), int(p.y*s+pad)))
	}
	if t.state == landing {
		b := t.bins[t.target]
		box = box.Union(image.Rect(int(b.x*s-pad), int(b.y*s-pad), int(b.x*s+pad), int(b.y*s+pad)))
	}
	return box
}

// render paints the frame at display scale s and returns the area that changed.
func (t *thrower) render(c *canvas, s, now float64) image.Rectangle {
	var base byte
	if t.catching() {
		base = 1
	}
	box := t.ballBox(s)
	dirty := box.Union(t.lastBox)
	if t.full {
		dirty = image.Rect(0, 0, c.w, c.h)
		t.full = false
	}
	t.lastBox = box
	dirty = dirty.Intersect(image.Rect(0, 0, c.w, c.h))
	c.clip = dirty
	c.clear(dirty, base)
	if t.state == idle || t.state == armed {
		return dirty
	}
	for i, b := range t.bins {
		x, y, r := b.x*s, b.y*s, binRadius*s
		c.disc(x, y+4*s, r, shadow, shadow)
		if i == t.hover || (t.state == landing && i == t.target) {
			c.disc(x, y, r, orangeLight, orange)
		} else {
			c.disc(x, y, r, glassLight, glass)
		}
		c.ring(x, y, r, 1.4*s, white)
		c.disc(x, y, r*0.52, mouth, mouth)
		c.text(t.formats[i], x, y, 15*s, white)
	}
	caption := ""
	switch t.state {
	case aiming:
		caption = "Throw it into a bin"
	case fizzling:
		caption = "Missed. Nothing changed."
	}
	if caption != "" {
		cx, cy := (t.area.left+t.area.right)/2*s, (t.area.bottom-binRadius*2-70)*s
		c.pill(image.Rect(int(cx-150*s), int(cy-17*s), int(cx+150*s), int(cy+17*s)), glassLight)
		c.text(caption, cx, cy, 13*s, ink)
	}
	if t.f == nil {
		return dirty
	}
	alpha := 1.0
	if t.state == fizzling {
		alpha = math.Max(0, 1-(now-t.since)/fadeTime)
	}
	x, y, r := t.f.x, t.f.y, ballRadius
	if t.state == landing {
		p := math.Min(1, (now-t.since)/landTime)
		b := t.bins[t.target]
		x, y, r = x+(b.x-x)*p, y+(b.y-y)*p, ballRadius*(1-p*0.8)
	}
	for i, p := range t.f.trail {
		k := float64(i+1) / float64(len(t.f.trail))
		c.disc(p.x*s, p.y*s, ballRadius*(0.35+0.5*k)*s, fade(orange, 0.35*k*alpha), fade(orange, 0.35*k*alpha))
	}
	c.disc(x*s, y*s, r*s, fade(orangeLight, alpha), fade(orange, alpha))
	c.ring(x*s, y*s, r*s, 1.5*s, fade(white, alpha))
	if r > 14 {
		c.text(t.ballLabel(), x*s, y*s, 12*s, fade(white, alpha))
	}
	return dirty
}
