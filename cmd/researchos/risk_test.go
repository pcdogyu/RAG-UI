package main

import (
	"math"
	"testing"
	"time"
)

func TestRiskLevelRequiresTwoSignals(t *testing.T) {
	cases := map[int]string{0: "OBSERVE", 1: "OBSERVE", 2: "MEDIUM", 3: "HIGH", 4: "CRITICAL", 5: "CRITICAL"}
	for count, expected := range cases {
		if got := riskLevel(count); got != expected {
			t.Fatalf("riskLevel(%d) = %q, want %q", count, got, expected)
		}
	}
}

func TestRiskHistoryRange(t *testing.T) {
	name, duration, ok := riskHistoryRange("")
	if !ok || name != "24h" || duration != 24*time.Hour {
		t.Fatalf("default history range = %q, %s, %v", name, duration, ok)
	}
	if _, _, ok := riskHistoryRange("2h"); ok {
		t.Fatal("unsupported history range was accepted")
	}
}

func TestTopForecastLevelsSeparatesLongAndShort(t *testing.T) {
	levels := topForecastLevels(
		[]float64{90, 95, 100, 105, 110, 115, 120},
		[][]float64{{2}, {9}, {100}, {8}, {4}, {7}, {6}},
		100,
	)
	longs, shorts := 0, 0
	for _, level := range levels {
		if level.Side == "long" {
			longs++
			if level.Price >= 100 {
				t.Fatalf("long forecast %v is not below price", level.Price)
			}
		}
		if level.Side == "short" {
			shorts++
			if level.Price <= 100 {
				t.Fatalf("short forecast %v is not above price", level.Price)
			}
		}
	}
	if longs != 2 || shorts != 3 {
		t.Fatalf("directional forecast levels = %d long, %d short", longs, shorts)
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
