package main

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
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
	venues, ok := exchangesFromQuery("binance,okx")
	if !ok || !venues["binance"] || !venues["okx"] {
		t.Fatalf("unexpected venues: %#v", venues)
	}
	if _, ok := exchangesFromQuery("kraken"); ok {
		t.Fatal("unexpected accepted venue")
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
