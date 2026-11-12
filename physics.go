package main

import "math"

// All physics runs in logical pixels (physical pixels / display scale).
const (
	armSpeed    = 1500.0 // px/s mid-drag that brings up the bins
	throwSpeed  = 900.0  // px/s at release that counts as a throw
	maxSpeed    = 6000.0 // clamp so a mouse glitch can't teleport the file
	friction    = 1.3    // velocity decay per second
	restitution = 0.6    // speed kept after bouncing off a screen edge
	ballRadius  = 26.0
	binRadius   = 64.0
	magnetRange = 230.0 // bins pull the file in from this far away
	magnet      = 9000.0
	funnel      = 4.0 // extra drag near a bin so the file settles instead of orbiting
	settleSpeed = 45.0
	maxFlight   = 4.0
)

type sample struct{ t, x, y float64 }

type rect struct{ left, top, right, bottom float64 }

type point struct{ x, y float64 }

// velocity is the cursor velocity over the last 90 ms, or zero if the hand had stopped.
func velocity(samples []sample, now float64) (vx, vy float64) {
	var recent []sample
	for _, s := range samples {
		if now-s.t <= 0.09 {
			recent = append(recent, s)
		}
	}
	if len(recent) < 2 || now-recent[len(recent)-1].t > 0.08 {
		return 0, 0
	}
	a, b := recent[0], recent[len(recent)-1]
	dt := b.t - a.t
	if dt < 0.004 {
		return 0, 0
	}
	vx, vy = (b.x-a.x)/dt, (b.y-a.y)/dt
	if s := math.Hypot(vx, vy); s > maxSpeed {
		vx, vy = vx*maxSpeed/s, vy*maxSpeed/s
	}
	return vx, vy
}

// layoutBins spreads n bins evenly around the inside of area, starting at top
// center and walking clockwise.
func layoutBins(n int, area rect) []point {
	inset := binRadius + 28
	l, t, r, b := area.left+inset, area.top+inset, area.right-inset, area.bottom-inset
	w, h := math.Max(r-l, 1), math.Max(b-t, 1)
	perimeter := 2 * (w + h)
	bins := make([]point, n)
	for i := range bins {
		s := math.Mod(w/2+float64(i)*perimeter/float64(n), perimeter)
		switch {
		case s < w:
			bins[i] = point{l + s, t}
		case s < w+h:
			bins[i] = point{r, t + s - w}
		case s < 2*w+h:
			bins[i] = point{r - (s - w - h), b}
		default:
			bins[i] = point{l, b - (s - 2*w - h)}
		}
	}
	return bins
}

func binAt(bins []point, x, y float64) int {
	for i, b := range bins {
		if math.Hypot(b.x-x, b.y-y) <= binRadius {
			return i
		}
	}
	return -1
}

const (
	flying = -1
	missed = -2
)

type flight struct {
	x, y, vx, vy, age float64
	trail             []point
}

// step advances dt seconds and returns flying, missed, or the caught bin index.
func (f *flight) step(dt float64, area rect, bins []point) int {
	// Substeps keep a fast throw from tunnelling straight through a bin.
	n := int(math.Max(1, math.Ceil(math.Hypot(f.vx, f.vy)*dt/12)))
	for i := 0; i < n; i++ {
		if r := f.substep(dt/float64(n), area, bins); r != flying {
			return r
		}
	}
	f.trail = append(f.trail, point{f.x, f.y})
	if len(f.trail) > 12 {
		f.trail = f.trail[1:]
	}
	return flying
}

func (f *flight) substep(dt float64, area rect, bins []point) int {
	f.age += dt
	near := false
	for i, b := range bins {
		dx, dy := b.x-f.x, b.y-f.y
		d := math.Hypot(dx, dy)
		if d <= binRadius-ballRadius*0.4 {
			return i
		}
		if d < magnetRange {
			near = true
			c := 1 - d/magnetRange
			f.vx += dx / d * magnet * c * dt
			f.vy += dy / d * magnet * c * dt
			k := math.Exp(-funnel * c * dt)
			f.vx, f.vy = f.vx*k, f.vy*k
		}
	}
	k := math.Exp(-friction * dt)
	f.vx, f.vy = f.vx*k, f.vy*k
	f.x += f.vx * dt
	f.y += f.vy * dt
	if f.x < area.left+ballRadius {
		f.x, f.vx = area.left+ballRadius, math.Abs(f.vx)*restitution
	}
	if f.x > area.right-ballRadius {
		f.x, f.vx = area.right-ballRadius, -math.Abs(f.vx)*restitution
	}
	if f.y < area.top+ballRadius {
		f.y, f.vy = area.top+ballRadius, math.Abs(f.vy)*restitution
	}
	if f.y > area.bottom-ballRadius {
		f.y, f.vy = area.bottom-ballRadius, -math.Abs(f.vy)*restitution
	}
	if f.age > maxFlight || (!near && math.Hypot(f.vx, f.vy) < settleSpeed) {
		return missed
	}
	return flying
}
