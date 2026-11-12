package main

import (
	"math"
	"testing"
)

var screen = rect{0, 0, 1920, 1040}

func fly(f *flight, bins []point) int {
	for i := 0; i < 400; i++ {
		if r := f.step(0.016, screen, bins); r != flying {
			return r
		}
	}
	return flying
}

func TestVelocityUsesRecentMotionOnly(t *testing.T) {
	s := []sample{{0, 0, 0}, {1, 0, 0}, {1.05, 100, 0}, {1.08, 160, 0}}
	if vx, vy := velocity(s, 1.09); math.Abs(vx-2000) > 1 || vy != 0 {
		t.Fatalf("got %v,%v", vx, vy)
	}
	if vx, vy := velocity(s, 1.3); vx != 0 || vy != 0 {
		t.Fatal("a hand that stopped before letting go is a drop, not a throw")
	}
	if vx, vy := velocity([]sample{{0, 0, 0}, {0.01, 5000, 5000}}, 0.01); math.Abs(math.Hypot(vx, vy)-maxSpeed) > 1 {
		t.Fatal("glitchy jumps should be clamped")
	}
}

func TestBinsFitAndDontOverlap(t *testing.T) {
	for n := 1; n <= 8; n++ {
		bins := layoutBins(n, screen)
		for i, a := range bins {
			if a.x < binRadius || a.x > screen.right-binRadius || a.y < binRadius || a.y > screen.bottom-binRadius {
				t.Fatalf("n=%d bin %d off screen: %v", n, i, a)
			}
			for _, b := range bins[i+1:] {
				if math.Hypot(a.x-b.x, a.y-b.y) <= 2*binRadius {
					t.Fatalf("n=%d bins overlap", n)
				}
			}
		}
	}
	if b := layoutBins(1, screen)[0]; b.x != 960 {
		t.Fatalf("one bin should sit top center, got %v", b)
	}
}

func TestAimedThrowsLand(t *testing.T) {
	bins := layoutBins(4, screen)
	for i, b := range bins {
		d := math.Hypot(b.x-960, b.y-600)
		if r := fly(&flight{x: 960, y: 600, vx: (b.x - 960) / d * 2500, vy: (b.y - 600) / d * 2500}, bins); r != i {
			t.Fatalf("throw at bin %d landed in %d", i, r)
		}
	}
}

func TestNearMissGetsPulledIn(t *testing.T) {
	if r := fly(&flight{x: 960 + 120, y: 600, vy: -2600}, []point{{960, 92}}); r != 0 {
		t.Fatalf("got %d", r)
	}
}

func TestFastThrowDoesNotTunnel(t *testing.T) {
	if r := fly(&flight{x: 960, y: 900, vy: -maxSpeed}, []point{{960, 300}}); r != 0 {
		t.Fatalf("got %d", r)
	}
}

func TestWildThrowBouncesAndMisses(t *testing.T) {
	f := &flight{x: 960, y: 520, vx: 3000}
	r := flying
	for i := 0; i < 400 && r == flying; i++ {
		r = f.step(0.016, screen, nil)
		if f.x < ballRadius || f.x > screen.right-ballRadius {
			t.Fatal("left the screen")
		}
	}
	if r != missed || f.vx >= 0 {
		t.Fatalf("want a bounce then a miss, got %d vx=%v", r, f.vx)
	}
}
