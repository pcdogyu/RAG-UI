package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testLiquidationService() *liquidationService {
	service := newLiquidationService(nil)
	service.byBinance["ETHUSDT"] = marketSymbol{Symbol: "ETH-USDT", BinanceSymbol: "ETHUSDT"}
	service.byOKX["ETH-USDT-SWAP"] = marketSymbol{Symbol: "ETH-USDT", OKXInstrumentID: "ETH-USDT-SWAP", OKXContractValue: 0.01}
	return service
}

func TestNormalizeBinanceForceOrder(t *testing.T) {
	event, ok := testLiquidationService().normalizeBinance("ETHUSDT", "SELL", "2500.5", "2", 1_700_000_000_000)
	if !ok {
		t.Fatal("expected a normalized event")
	}
	if event.Symbol != "ETH-USDT" || event.Side != "long" {
		t.Fatalf("unexpected event identity: %+v", event)
	}
	if event.Notional != 5001 {
		t.Fatalf("notional = %v, want 5001", event.Notional)
	}
	if event.Occurred != time.UnixMilli(1_700_000_000_000).UTC() {
		t.Fatalf("unexpected timestamp: %s", event.Occurred)
	}
}

func TestNormalizeBinanceAllMarketForceOrder(t *testing.T) {
	service := testLiquidationService()
	raw := json.RawMessage(`[{"e":"forceOrder","o":{"s":"ETHUSDT","S":"SELL","ap":"2500.5","z":"2","T":1700000000000}}]`)
	events := service.normalizeBinanceForceOrders(raw)
	if len(events) != 1 || events[0].Symbol != "ETH-USDT" || events[0].Side != "long" {
		t.Fatalf("unexpected all-market events: %+v", events)
	}
}

func TestFallbackEventMatchesDirectDedupKey(t *testing.T) {
	service := testLiquidationService()
	direct, ok := service.normalizeBinance("ETHUSDT", "SELL", "2500.5", "2", 1_700_000_000_000)
	if !ok {
		t.Fatal("expected direct event")
	}
	fallback, ok := service.normalizeFallback(fallbackLiquidationRow{Exchange: "binance", Symbol: "ETHUSDT", Side: "long", Price: 2500.5, Quantity: 2, Notional: 5001, EventTS: 1_700_000_000_000})
	if !ok {
		t.Fatal("expected fallback event")
	}
	if fallback.DedupKey != direct.DedupKey {
		t.Fatalf("fallback key = %q, direct key = %q", fallback.DedupKey, direct.DedupKey)
	}
	if _, ok := service.normalizeFallback(fallbackLiquidationRow{Exchange: "bybit", Symbol: "ETHUSDT", Side: "long", Price: 1, Quantity: 1, EventTS: 1}); ok {
		t.Fatal("bybit must remain out of scope")
	}
}

func TestFallbackSyncReadsPagesUntilWindowBoundary(t *testing.T) {
	service := testLiquidationService()
	requestedPages := make([]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPages = append(requestedPages, r.URL.Query().Get("page"))
		if r.URL.Query().Get("page") == "1" {
			rows := make([]fallbackLiquidationRow, 1000)
			for i := range rows {
				rows[i] = fallbackLiquidationRow{Exchange: "binance", Symbol: "ETHUSDT", Side: "long", Price: 2500, Quantity: 1, EventTS: 1_700_000_100_000}
			}
			_ = json.NewEncoder(w).Encode(fallbackLiquidationResponse{Rows: rows})
			return
		}
		_ = json.NewEncoder(w).Encode(fallbackLiquidationResponse{Rows: []fallbackLiquidationRow{{Exchange: "binance", Symbol: "ETHUSDT", Side: "long", Price: 2500, Quantity: 1, EventTS: 1_699_999_900_000}}})
	}))
	defer server.Close()
	service.fallbackURL = server.URL
	if err := service.syncFallback(context.Background(), time.UnixMilli(1_700_000_000_000).UTC()); err != nil {
		t.Fatalf("sync fallback: %v", err)
	}
	if strings.Join(requestedPages, ",") != "1,2" {
		t.Fatalf("pages = %v, want [1 2]", requestedPages)
	}
	if service.statusSnapshot().Fallback.LastSuccess.IsZero() {
		t.Fatal("fallback success was not recorded")
	}
}

func TestDirectStatusSeparatesRawFramesAndParseErrors(t *testing.T) {
	service := testLiquidationService()
	service.markDirectRawMessage("binance")
	service.recordDirectParseError("binance", context.DeadlineExceeded)
	status := service.statusSnapshot().Exchanges["binance"]
	if status.LastRawMessage.IsZero() {
		t.Fatal("raw message time was not recorded")
	}
	if status.LastParseError != context.DeadlineExceeded.Error() {
		t.Fatalf("parse error = %q", status.LastParseError)
	}
	if status.Error != "" {
		t.Fatalf("connection error must remain separate, got %q", status.Error)
	}
}

func TestNormalizeOKXLiquidationUsesContractValue(t *testing.T) {
	event, ok := testLiquidationService().normalizeOKX("ETH-USDT-SWAP", "buy", 2500, 3, 1_700_000_000_000)
	if !ok {
		t.Fatal("expected a normalized event")
	}
	if event.Side != "short" {
		t.Fatalf("side = %q, want short", event.Side)
	}
	if math.Abs(event.Notional-75) > 0.0001 {
		t.Fatalf("notional = %v, want 75", event.Notional)
	}
}

func TestNormalizeOKXEnvelope(t *testing.T) {
	service := testLiquidationService()
	raw := json.RawMessage(`{"instId":"ETH-USDT-SWAP","details":[{"bkPx":"2500","sz":"3","side":"sell","ts":"1700000000000"}]}`)
	events := service.normalizeOKXLiquidations(raw)
	if len(events) != 1 || events[0].Side != "long" {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestRangeAndVenueValidation(t *testing.T) {
	_, window, ok := rangeStart("7d")
	if !ok || window != "7d" {
		t.Fatalf("range = %q, %t", window, ok)
	}
	if _, _, ok := rangeStart("90d"); ok {
		t.Fatal("unexpected accepted range")
	}
	for _, value := range []string{"8h", "12h"} {
		if _, window, ok := rangeStart(value); !ok || window != value {
			t.Fatalf("range %q was not accepted", value)
		}
	}
	venues, ok := exchangesFromQuery("binance,okx")
	if !ok || !venues["binance"] || !venues["okx"] {
		t.Fatalf("unexpected venues: %#v", venues)
	}
	if _, ok := exchangesFromQuery("kraken"); ok {
		t.Fatal("unexpected accepted venue")
	}
}

func TestLiquidationFilterValidationAndMatching(t *testing.T) {
	notional, ok := parseLiquidationFilter(map[string][]string{"filter": {"notional"}, "min": {"5000"}})
	if !ok || !notional.matches(liquidationEvent{Price: 2500, Notional: 5000}) || notional.matches(liquidationEvent{Price: 2500, Notional: 4999}) {
		t.Fatalf("unexpected notional filter behavior: %+v", notional)
	}
	quantity, ok := parseLiquidationFilter(map[string][]string{"filter": {"quantity"}, "min": {"1"}})
	if !ok || !quantity.matches(liquidationEvent{Price: 2500, Notional: 2500}) || quantity.matches(liquidationEvent{Price: 2500, Notional: 2499}) {
		t.Fatalf("unexpected quantity filter behavior: %+v", quantity)
	}
	for _, values := range []url.Values{
		{"filter": {"quantity"}, "min": {"0"}},
		{"filter": {"notional"}, "min": {"NaN"}},
		{"filter": {"unknown"}, "min": {"1"}},
		{"filter": {"quantity"}},
	} {
		if _, ok := parseLiquidationFilter(values); ok {
			t.Fatalf("expected invalid filter: %#v", values)
		}
	}
}

func TestRetentionDuration(t *testing.T) {
	t.Setenv("LIQUIDATION_RETENTION_HOURS", "48")
	if got := retentionDuration(); got != 48*time.Hour {
		t.Fatalf("retention = %s, want 48h", got)
	}
}

func TestLiquidationHTTPStatusWithoutDatabase(t *testing.T) {
	service := testLiquidationService()
	service.symbols = []marketSymbol{{Symbol: "ETH-USDT"}}

	symbolsRequest := httptest.NewRequest(http.MethodGet, "/api/v1/liquidations/symbols", nil)
	symbolsResponse := httptest.NewRecorder()
	service.serveSymbols(symbolsResponse, symbolsRequest)
	if symbolsResponse.Code != http.StatusOK || !strings.Contains(symbolsResponse.Body.String(), "ETH-USDT") {
		t.Fatalf("unexpected symbols response: %d %s", symbolsResponse.Code, symbolsResponse.Body.String())
	}

	chartRequest := httptest.NewRequest(http.MethodGet, "/api/v1/liquidations/chart?symbol=ETH-USDT", nil)
	chartResponse := httptest.NewRecorder()
	service.serveChart(chartResponse, chartRequest)
	if chartResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("chart status = %d, want %d", chartResponse.Code, http.StatusServiceUnavailable)
	}
}
