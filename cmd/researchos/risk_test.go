package main

import (
	"math"
	"testing"
)

func TestRiskLevelRequiresTwoSignals(t *testing.T) {
	cases := map[int]string{0: "OBSERVE", 1: "OBSERVE", 2: "MEDIUM", 3: "HIGH", 4: "CRITICAL", 5: "CRITICAL"}
	for count, expected := range cases {
		if got := riskLevel(count); got != expected {
			t.Fatalf("riskLevel(%d) = %q, want %q", count, got, expected)
		}
	}
}

func TestZScoreUsesDynamicBaseline(t *testing.T) {
	if got := zScore(15, []float64{10, 10, 10}); got != 0 {
		t.Fatalf("zero-variance z-score = %v, want 0", got)
	}
	got := zScore(14, []float64{8, 10, 12})
	if math.Abs(got-2.449489742783178) > 0.000001 {
		t.Fatalf("z-score = %v", got)
	}
}

func TestTradeDeltaOnlyCountsNewTrades(t *testing.T) {
	service := newRiskService(nil, nil)
	first := service.newTradeDelta("binance:ETHUSDT", func(last int64) (int64, float64) {
		if last != 0 {
			t.Fatalf("first cursor = %d", last)
		}
		return 101, 42
	})
	second := service.newTradeDelta("binance:ETHUSDT", func(last int64) (int64, float64) {
		if last != 101 {
			t.Fatalf("second cursor = %d", last)
		}
		return 102, -7
	})
	if first != 42 || second != -7 {
		t.Fatalf("deltas = %v, %v", first, second)
	}
}
