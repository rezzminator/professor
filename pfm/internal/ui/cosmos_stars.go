package ui

import (
	"math"
	"math/rand"
	"sync"
	"time"
)

type star struct {
	px, py int
	phase  float64
	speed  float64
}

// poleStar is one of the bright stars that flare into a four-point sparkle.
type poleStar struct {
	px, py int
	phase  float64
}

// starTable is the sky's layout for one canvas size: where every star sits and
// its own twinkle phase and speed. The seeds are fixed, so the layout is a pure
// function of the size; only the brightness moves with the clock. Seeding three
// generators on every frame cost more than drawing the stars.
type starTable struct {
	cols, rows int
	far, main  []star
	poles      [4]poleStar
}

var (
	starTableMu     sync.Mutex
	starTableCached *starTable
	// starTableBuilds counts how often the layout was derived from its seeds;
	// a test pins that a steady canvas size derives it once.
	starTableBuilds int
)

// starTableFor returns the layout for the canvas size, deriving it when the
// size changed since the last frame.
func starTableFor(cols, rows int) *starTable {
	starTableMu.Lock()
	defer starTableMu.Unlock()
	if table := starTableCached; table != nil && table.cols == cols && table.rows == rows {
		return table
	}
	starTableBuilds++
	table := &starTable{cols: cols, rows: rows}
	pw, ph := cols*2, rows*4
	// Far layer: dimmer, slower, sparser — depth behind the main twinkle.
	far := rand.New(rand.NewSource(56))
	for range cols * rows / 32 {
		px, py := far.Intn(pw), far.Intn(ph)
		phase := far.Float64() * 2 * math.Pi
		speed := 0.2 + far.Float64()*0.5
		table.far = append(table.far, star{px: px, py: py, phase: phase, speed: speed})
	}
	rng := rand.New(rand.NewSource(55))
	for range cols * rows / 20 {
		table.main = append(table.main, star{
			px: rng.Intn(pw), py: rng.Intn(ph),
			phase: rng.Float64() * 2 * math.Pi,
			speed: 0.6 + rng.Float64()*1.8,
		})
	}
	poles := rand.New(rand.NewSource(57))
	for index := range table.poles {
		px, py := poles.Intn(pw), poles.Intn(ph)
		table.poles[index] = poleStar{px: px, py: py, phase: poles.Float64() * 2 * math.Pi}
	}
	starTableCached = table
	return table
}

func drawCosmosStars(canvas *Canvas, now time.Time) {
	t := float64(now.UnixNano()) / 1e9
	starColor := rgbFromHex(configuredPalette.CosmosStar)
	table := starTableFor(canvas.Cols, canvas.Rows)
	for _, far := range table.far {
		canvas.Dot(far.px, far.py, scaleRGB(starColor, 0.10+0.16*(0.5+0.5*math.Sin(t*far.speed+far.phase))))
	}
	for _, star := range table.main {
		brightness := 0.18 + 0.42*(0.5+0.5*math.Sin(t*star.speed+star.phase))
		canvas.Dot(star.px, star.py, scaleRGB(starColor, brightness))
	}
	// A few bright stars that flare into a four-point sparkle at their peak.
	bright := rgbFromHex(configuredPalette.CosmosBright)
	for _, pole := range table.poles {
		px, py := pole.px, pole.py
		glow := 0.5 + 0.5*math.Sin(t*0.7+pole.phase)
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
