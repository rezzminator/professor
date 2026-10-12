package ui

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

func drawCosmosStarsReference(canvas *Canvas, now time.Time) {
	t := float64(now.UnixNano()) / 1e9
	starColor := rgbFromHex(configuredPalette.CosmosStar)
	// Far layer: dimmer, slower, sparser — depth behind the main twinkle.
	far := rand.New(rand.NewSource(56))
	for range canvas.Cols * canvas.Rows / 32 {
		px, py := far.Intn(canvas.PW()), far.Intn(canvas.PH())
		phase := far.Float64() * 2 * math.Pi
		speed := 0.2 + far.Float64()*0.5
		canvas.Dot(px, py, scaleRGB(starColor, 0.10+0.16*(0.5+0.5*math.Sin(t*speed+phase))))
	}
	rng := rand.New(rand.NewSource(55))
	count := canvas.Cols * canvas.Rows / 20
	for range count {
		star := star{
			px: rng.Intn(canvas.PW()), py: rng.Intn(canvas.PH()),
			phase: rng.Float64() * 2 * math.Pi,
			speed: 0.6 + rng.Float64()*1.8,
		}
		brightness := 0.18 + 0.42*(0.5+0.5*math.Sin(t*star.speed+star.phase))
		canvas.Dot(star.px, star.py, scaleRGB(starColor, brightness))
	}
	// A few bright stars that flare into a four-point sparkle at their peak.
	bright := rgbFromHex(configuredPalette.CosmosBright)
	poles := rand.New(rand.NewSource(57))
	for range 4 {
		px, py := poles.Intn(canvas.PW()), poles.Intn(canvas.PH())
		phase := poles.Float64() * 2 * math.Pi
		glow := 0.5 + 0.5*math.Sin(t*0.7+phase)
		canvas.Dot(px, py, scaleRGB(bright, 0.20+0.50*glow))
		if glow > 0.75 {
			cross := scaleRGB(bright, (glow-0.75)*2.2)
			canvas.Dot(px+1, py, cross)
			canvas.Dot(px-1, py, cross)
			canvas.Dot(px, py+1, cross)
			canvas.Dot(px, py-1, cross)
		}
	}
	drawCosmosMeteor(canvas, now)
}

// TestDrawCosmosStarsMatchesTheSeededReferenceFrameForFrame pins that the star
// layout derived once per canvas size draws exactly the sky the seeded
// generators drew on every frame: same dots, same colours, same order of
// ties, across sizes and clock instants.
func TestDrawCosmosStarsMatchesTheSeededReferenceFrameForFrame(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	for _, size := range [][2]int{{118, 43}, {38, 8}, {200, 60}, {118, 43}, {3, 3}} {
		for step := range 40 {
			now := base.Add(time.Duration(step) * 66 * time.Millisecond)
			got, want := NewCanvas(size[0], size[1]), NewCanvas(size[0], size[1])
			drawCosmosStars(got, now)
			drawCosmosStarsReference(want, now)
			if got.render() != want.render() {
				t.Fatalf("%dx%d frame %d: the sky differs from the seeded reference", size[0], size[1], step)
			}
		}
	}
}

// TestDrawCosmosStarsDerivesItsLayoutOncePerCanvasSize pins the removed work: a
// sky redrawn at the same canvas size reseeds three generators (a few hundred
// microseconds each) on every one of the ~15 frames a second; the layout is a
// pure function of the size.
func TestDrawCosmosStarsDerivesItsLayoutOncePerCanvasSize(t *testing.T) {
	canvas := NewCanvas(97, 31)
	drawCosmosStars(canvas, time.Unix(1_800_000_000, 0))
	before := starTableBuilds
	for step := range 20 {
		drawCosmosStars(canvas, time.Unix(1_800_000_000, int64(step)*int64(66*time.Millisecond)))
	}
	if starTableBuilds != before {
		t.Fatalf("the star layout was derived %d more times for 20 frames at one size, want 0", starTableBuilds-before)
	}
	drawCosmosStars(NewCanvas(98, 31), time.Unix(1_800_000_000, 0))
	if starTableBuilds != before+1 {
		t.Fatalf("a new canvas size derived the layout %d times, want 1", starTableBuilds-before)
	}
}
