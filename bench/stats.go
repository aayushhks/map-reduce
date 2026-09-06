package main

import (
	"math"
	"sort"
	"time"
)

// Distribution summarises a set of samples.
type Distribution struct {
	Count int     `json:"count"`
	Min   float64 `json:"min_ms"`
	P50   float64 `json:"p50_ms"`
	P95   float64 `json:"p95_ms"`
	P99   float64 `json:"p99_ms"`
	Max   float64 `json:"max_ms"`
	Mean  float64 `json:"mean_ms"`
}

// describe summarises durations in milliseconds.
func describe(samples []time.Duration) Distribution {
	if len(samples) == 0 {
		return Distribution{}
	}

	ms := make([]float64, len(samples))
	total := 0.0
	for i, s := range samples {
		ms[i] = float64(s) / float64(time.Millisecond)
		total += ms[i]
	}
	sort.Float64s(ms)

	return Distribution{
		Count: len(ms),
		Min:   ms[0],
		P50:   percentile(ms, 50),
		P95:   percentile(ms, 95),
		P99:   percentile(ms, 99),
		Max:   ms[len(ms)-1],
		Mean:  round(total / float64(len(ms))),
	}
}

// percentile returns the nearest rank percentile of an ascending slice.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return round(sorted[rank-1])
}

// medianOf returns the median of a set of trial measurements.
func medianOf(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)

	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return round(sorted[mid])
	}
	return round((sorted[mid-1] + sorted[mid]) / 2)
}

// minMax returns the smallest and largest of a set of measurements.
func minMax(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	lo, hi := values[0], values[0]
	for _, v := range values {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	return round(lo), round(hi)
}

// round trims a measurement to three decimal places for stable JSON output.
func round(v float64) float64 {
	return math.Round(v*1000) / 1000
}
