package main

import (
	"math"
	"testing"
	"time"
)

func TestProfileExcludesRampAndTail(t *testing.T) {
	var p speedProfile
	p.sample(0, 0)
	var bytes int64
	for i := 1; i <= 100; i++ {
		factor := 1.0
		if i < 15 {
			factor = float64(i) / 15
		} else if i > 85 {
			factor = 0.2
		}
		bytes += int64(1000 * 1e6 / 8 * 0.1 * factor)
		p.sample(time.Duration(i)*sampleInterval, bytes)
	}
	r := p.result(10*time.Second, bytes)
	if !r.Measured || math.Abs(r.AvgMbps-1000) > 0.01 || math.Abs(r.PeakMbps-1000) > 0.01 {
		t.Fatalf("ramp/tail distorted steady speed: %+v", r)
	}
	if r.From < 1500*time.Millisecond || r.To > 8500*time.Millisecond {
		t.Fatalf("measurement escaped steady window: %+v", r)
	}
}

func TestProfileUsesActualSampleIntervals(t *testing.T) {
	var p speedProfile
	for _, ms := range []int{0, 100, 300, 1000, 1600, 1750, 2300, 2800, 3300, 3600, 4000, 4700, 5100} {
		elapsed := time.Duration(ms) * time.Millisecond
		p.sample(elapsed, int64(125e6*elapsed.Seconds()))
	}
	r := p.result(5100*time.Millisecond, int64(125e6*5.1))
	if !r.Measured || math.Abs(r.AvgMbps-1000) > 0.01 {
		t.Fatalf("irregular samples should still measure 1000 Mbps: %+v", r)
	}
}

func TestProfilePeakRejectsSingleSpike(t *testing.T) {
	var samples []speedSample
	var bytes int64
	for i := 0; i < 50; i++ {
		bytes += 6250000 // 500 Mbps at 100 ms
		if i == 25 {
			bytes += 625000000
		}
		samples = append(samples, speedSample{time.Duration(i) * sampleInterval, bytes})
	}
	if peak := filteredPeak(samples); math.Abs(peak-500) > 0.01 {
		t.Fatalf("single spike inflated peak to %v", peak)
	}
}

func TestProfileInsufficientSamples(t *testing.T) {
	var p speedProfile
	p.sample(0, 0)
	p.sample(time.Millisecond, 100)
	if r := p.result(time.Millisecond, 100); r.Measured || r.Bytes != 100 {
		t.Fatalf("short run must preserve bytes without fabricating speed: %+v", r)
	}
}
