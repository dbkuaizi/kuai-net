package main

import (
	"math"
	"slices"
	"time"
)

const sampleInterval = 100 * time.Millisecond

type speedSample struct {
	Elapsed time.Duration
	Bytes   int64
}

type rateSample struct {
	Elapsed time.Duration
	Mbps    float64
}

type speedResult struct {
	AvgMbps, PeakMbps float64
	Bytes             int64
	Elapsed           time.Duration
	From, To          time.Duration
	Measured          bool
	Stopped           bool
}

// The window boundaries, smoothing and peak filter follow static/profile.js.
// Each calculation uses actual timestamps, so delayed ticks do not inflate speed.
type speedProfile struct {
	samples  []speedSample
	window   []rateSample
	smoothed float64
	warmed   bool
}

func (p *speedProfile) sample(elapsed time.Duration, bytes int64) float64 {
	point := speedSample{elapsed, bytes}
	if len(p.samples) == 0 {
		p.samples = append(p.samples, point)
		return 0
	}
	last := p.samples[len(p.samples)-1]
	if elapsed <= last.Elapsed {
		return p.smoothed
	}
	p.samples = append(p.samples, point)
	if elapsed-last.Elapsed <= time.Millisecond {
		return p.smoothed
	}
	raw := float64(max(0, bytes-last.Bytes)) * 8 / (elapsed - last.Elapsed).Seconds() / 1e6
	if elapsed-p.samples[0].Elapsed < 1500*time.Millisecond {
		return raw
	}
	p.window = append(p.window, rateSample{elapsed, raw})
	for len(p.window) > 2 && elapsed-p.window[0].Elapsed > 1500*time.Millisecond {
		p.window = p.window[1:]
	}
	var sum float64
	for _, point := range p.window {
		sum += point.Mbps
	}
	avg := sum / float64(len(p.window))
	if !p.warmed {
		p.smoothed, p.warmed = avg, true
	} else {
		p.smoothed = 0.35*avg + 0.65*p.smoothed
	}
	return p.smoothed
}

func (p *speedProfile) result(elapsed time.Duration, bytes int64) speedResult {
	result := speedResult{Bytes: bytes, Elapsed: elapsed}
	if len(p.samples) < 2 {
		return result
	}
	start := p.samples[0].Elapsed
	duration := elapsed - start
	head := start + min(1500*time.Millisecond, time.Duration(float64(duration)*0.6))
	tail := min(max(time.Duration(float64(duration)*0.15), 500*time.Millisecond), duration/2)
	end := max(head+time.Millisecond, elapsed-tail)
	var body []speedSample
	for _, point := range p.samples {
		if point.Elapsed >= head && point.Elapsed <= end {
			body = append(body, point)
		}
	}
	if len(body) < 2 {
		body = nil
		for _, point := range p.samples {
			if point.Elapsed >= head {
				body = append(body, point)
			}
		}
	}
	if len(body) < 2 {
		return result
	}
	a, b := body[0], body[len(body)-1]
	if len(body) >= 20 {
		k := len(body) * 5 / 100
		a, b = body[k], body[len(body)-1-k]
	}
	if b.Elapsed <= a.Elapsed {
		return result
	}
	result.From, result.To = a.Elapsed-start, b.Elapsed-start
	result.AvgMbps = float64(max(0, b.Bytes-a.Bytes)) * 8 / (b.Elapsed - a.Elapsed).Seconds() / 1e6
	result.PeakMbps = math.Max(result.AvgMbps, filteredPeak(body))
	result.Measured = true
	return result
}

func filteredPeak(points []speedSample) float64 {
	if len(points) < 4 {
		return 0
	}
	rates := make([]rateSample, 0, len(points)-1)
	for i := 1; i < len(points); i++ {
		dt := points[i].Elapsed - points[i-1].Elapsed
		if dt > 0 {
			rates = append(rates, rateSample{points[i].Elapsed,
				float64(max(0, points[i].Bytes-points[i-1].Bytes)) * 8 / dt.Seconds() / 1e6})
		}
	}
	filtered := make([]rateSample, len(rates))
	for i, point := range rates {
		var window []float64
		for j := max(0, i-2); j <= min(len(rates)-1, i+2); j++ {
			window = append(window, rates[j].Mbps)
		}
		slices.Sort(window)
		filtered[i] = rateSample{point.Elapsed, window[len(window)/2]}
	}
	var peak, sum float64
	lo := 0
	for hi, point := range filtered {
		sum += point.Mbps
		for lo < hi && point.Elapsed-filtered[lo].Elapsed > time.Second {
			sum -= filtered[lo].Mbps
			lo++
		}
		peak = math.Max(peak, sum/float64(hi-lo+1))
	}
	return peak
}
