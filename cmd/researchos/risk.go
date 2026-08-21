package main

// The risk radar deliberately uses only public market data.  It describes
// observed liquidation density, not an exchange's private liquidation book.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	riskSnapshotInterval = 5 * time.Minute
	riskEvaluationEvery  = 30 * time.Second
	riskBaselineWindow   = 24 * time.Hour
	riskAlertCooldown    = 15 * time.Minute
	riskETHSymbol        = "ETH-USDT"
	riskForecastCacheTTL = 5 * time.Minute
	riskForecastWindow   = 30
)

type riskSnapshot struct {
	Symbol       string    `json:"symbol"`
	ObservedAt   time.Time `json:"observed_at"`
	MarkPrice    float64   `json:"mark_price"`
	SpotPrice    float64   `json:"spot_price"`
	OIUSD        float64   `json:"oi_usd"`
	FundingRate  float64   `json:"funding_rate"`
	CVDDeltaUSD  float64   `json:"cvd_delta_usd"`
	CVDTotalUSD  float64   `json:"cvd_total_usd"`
	BasisPct     float64   `json:"basis_pct"`
	VenueCount   int       `json:"venue_count"`
	Availability string    `json:"availability,omitempty"`
}

type riskZone struct {
	Side        string  `json:"side"`
	Price       float64 `json:"price"`
	NotionalUSD float64 `json:"notional_usd"`
	DistancePct float64 `json:"distance_pct"`
}

type riskForecastLevel struct {
	Side        string  `json:"side"`
	Price       float64 `json:"price"`
	Intensity   float64 `json:"intensity"`
	DistancePct float64 `json:"distance_pct"`
}

type riskForecastBucket struct {
	Price     float64
	Intensity float64
}

type riskForecast struct {
	Levels    []riskForecastLevel `json:"levels"`
	Status    string              `json:"status"`
	UpdatedAt *time.Time          `json:"updated_at,omitempty"`
	Source    string              `json:"source"`
}

type riskHistoryResponse struct {
	Symbol    string         `json:"symbol"`
	Range     string         `json:"range"`
	Snapshots []riskSnapshot `json:"snapshots"`
	Status    string         `json:"status"`
}

type riskSignal struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Active   bool   `json:"active"`
	Severity string `json:"severity"`
}

type riskAlertEvent struct {
	ID             int64        `json:"id"`
	Symbol         string       `json:"symbol"`
	Level          string       `json:"level"`
	TriggerCount   int          `json:"trigger_count"`
	Signals        []riskSignal `json:"signals"`
	Snapshot       riskSnapshot `json:"snapshot"`
	CreatedAt      time.Time    `json:"created_at"`
	TelegramStatus string       `json:"telegram_status"`
	TelegramError  string       `json:"telegram_error,omitempty"`
}

type riskRadarResponse struct {
	Snapshot       riskSnapshot     `json:"snapshot"`
	Zones          []riskZone       `json:"zones"`
	ObservedLevels []riskZone       `json:"observed_levels"`
	Forecast       riskForecast     `json:"forecast"`
	Signals        []riskSignal     `json:"signals"`
	Events         []riskAlertEvent `json:"events"`
	Status         string           `json:"status"`
}

type riskService struct {
	store        *liquidationStore
	liquidations *liquidationService
	client       *http.Client
	mu           sync.RWMutex
	latest       map[string]riskSnapshot
	previousZone map[string]riskZone
	tradeCursor  map[string]int64
	cvdTotal     map[string]float64
	cvdSeeded    map[string]bool
	forecastMu   sync.Mutex
	forecastAt   time.Time
	forecastData []riskForecastBucket
	forecastErr  string
	forecastTime time.Time
}

func newRiskService(store *liquidationStore, liquidations *liquidationService) *riskService {
	return &riskService{store: store, liquidations: liquidations, client: &http.Client{Timeout: 12 * time.Second}, latest: map[string]riskSnapshot{}, previousZone: map[string]riskZone{}, tradeCursor: map[string]int64{}, cvdTotal: map[string]float64{}, cvdSeeded: map[string]bool{}}
}

func (s *riskService) getJSON(ctx context.Context, endpoint string, result any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("HTTP %s", response.Status)
	}
	return json.NewDecoder(response.Body).Decode(result)
}

func (s *riskService) start(ctx context.Context) {
	if s.store == nil {
		return
	}
	go func() {
		s.refresh(ctx)
		ticker := time.NewTicker(riskSnapshotInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.refresh(ctx)
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(riskEvaluationEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.evaluateAll(ctx)
			}
		}
	}()
}

func (s *riskService) refresh(ctx context.Context) {
	if s.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 55*time.Second)
	defer cancel()
	binance, spots, okx, err := s.marketBooks(ctx)
	if err != nil {
		return
	}
	symbol := marketSymbol{Symbol: riskETHSymbol, BinanceSymbol: "ETHUSDT", OKXInstrumentID: "ETH-USDT-SWAP"}
	s.ensureCVDSeed(ctx, symbol.Symbol)
	snapshot, ok := s.collectSnapshot(ctx, symbol, binance, spots, okx)
	if !ok || s.saveSnapshot(ctx, snapshot) != nil {
		return
	}
	s.mu.Lock()
	s.latest[symbol.Symbol] = snapshot
	s.mu.Unlock()
	s.evaluateAll(ctx)
	// Keep the previous refresh's zone until evaluation has completed so a
	// migration is measured against a real prior observation.
	s.mu.RLock()
	snapshots := make([]riskSnapshot, 0, len(s.latest))
	for _, item := range s.latest {
		snapshots = append(snapshots, item)
	}
	s.mu.RUnlock()
	for _, snapshot := range snapshots {
		if zones, err := s.zones(ctx, snapshot.Symbol, snapshot.MarkPrice); err == nil && len(zones) > 0 {
			s.mu.Lock()
			s.previousZone[snapshot.Symbol] = zones[0]
			s.mu.Unlock()
		}
	}
}

// ensureCVDSeed preserves the displayed cumulative aggressive flow across a
// process restart. The trade cursor is intentionally fresh, but the running
// CVD total continues from the latest persisted observation.
func (s *riskService) ensureCVDSeed(ctx context.Context, symbol string) {
	s.mu.RLock()
	seeded := s.cvdSeeded[symbol]
	s.mu.RUnlock()
	if seeded {
		return
	}
	var total sql.NullFloat64
	err := s.store.db.QueryRowContext(ctx, `SELECT cvd_total_usd FROM risk_market_snapshots WHERE symbol=$1 ORDER BY observed_at DESC LIMIT 1`, symbol).Scan(&total)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cvdSeeded[symbol] {
		return
	}
	if err == nil && total.Valid {
		s.cvdTotal[symbol] = total.Float64
	}
	s.cvdSeeded[symbol] = true
}

type binanceMarket struct{ Mark, Funding float64 }
type okxMarket struct{ Mark, OI, Funding float64 }

func (s *riskService) marketBooks(ctx context.Context) (map[string]binanceMarket, map[string]float64, map[string]okxMarket, error) {
	var futures []struct {
		Symbol          string `json:"symbol"`
		MarkPrice       string `json:"markPrice"`
		LastFundingRate string `json:"lastFundingRate"`
	}
	var spotRows []struct {
		Symbol string `json:"symbol"`
		Price  string `json:"price"`
	}
	var okxTickers struct {
		Data []struct {
			InstID string `json:"instId"`
			Last   string `json:"last"`
		} `json:"data"`
	}
	var okxOI struct {
		Data []struct {
			InstID string `json:"instId"`
			OICcy  string `json:"oiCcy"`
		} `json:"data"`
	}
	if err := s.getJSON(ctx, "https://fapi.binance.com/fapi/v1/premiumIndex", &futures); err != nil {
		return nil, nil, nil, err
	}
	if err := s.getJSON(ctx, "https://api.binance.com/api/v3/ticker/price", &spotRows); err != nil {
		return nil, nil, nil, err
	}
	if err := s.getJSON(ctx, "https://www.okx.com/api/v5/market/tickers?instType=SWAP", &okxTickers); err != nil {
		return nil, nil, nil, err
	}
	if err := s.getJSON(ctx, "https://www.okx.com/api/v5/public/open-interest?instType=SWAP", &okxOI); err != nil {
		return nil, nil, nil, err
	}
	binance := map[string]binanceMarket{}
	for _, row := range futures {
		binance[row.Symbol] = binanceMarket{Mark: number(row.MarkPrice), Funding: number(row.LastFundingRate)}
	}
	spots := map[string]float64{}
	for _, row := range spotRows {
		spots[row.Symbol] = number(row.Price)
	}
	okx := map[string]okxMarket{}
	for _, row := range okxTickers.Data {
		item := okx[row.InstID]
		item.Mark = number(row.Last)
		okx[row.InstID] = item
	}
	for _, row := range okxOI.Data {
		item := okx[row.InstID]
		item.OI = number(row.OICcy) * item.Mark
		okx[row.InstID] = item
	}
	return binance, spots, okx, nil
}

func (s *riskService) collectSnapshot(ctx context.Context, symbol marketSymbol, binance map[string]binanceMarket, spots map[string]float64, okx map[string]okxMarket) (riskSnapshot, bool) {
	var mark, spot, oi, fundingWeight, funding, cvd float64
	venues := 0
	if item, exists := binance[symbol.BinanceSymbol]; exists && item.Mark > 0 {
		binanceOI, delta := s.binanceMetrics(ctx, symbol.BinanceSymbol, item.Mark)
		mark += item.Mark
		oi += binanceOI
		funding += item.Funding * math.Max(binanceOI, 1)
		fundingWeight += math.Max(binanceOI, 1)
		cvd += delta
		venues++
		spot += spots[symbol.BinanceSymbol]
	}
	if item, exists := okx[symbol.OKXInstrumentID]; exists && item.Mark > 0 {
		okxFunding, delta := s.okxMetrics(ctx, symbol.OKXInstrumentID)
		mark += item.Mark
		oi += item.OI
		funding += okxFunding * math.Max(item.OI, 1)
		fundingWeight += math.Max(item.OI, 1)
		cvd += delta
		venues++
		spotSymbol := strings.ReplaceAll(strings.TrimSuffix(symbol.OKXInstrumentID, "-SWAP"), "-", "")
		spot += spots[spotSymbol]
	}
	if venues == 0 || mark <= 0 {
		return riskSnapshot{}, false
	}
	mark /= float64(venues)
	if spot <= 0 {
		spot = mark
	} else {
		spot /= float64(venues)
	}
	s.mu.Lock()
	s.cvdTotal[symbol.Symbol] += cvd
	total := s.cvdTotal[symbol.Symbol]
	s.mu.Unlock()
	fundingRate := 0.0
	if fundingWeight > 0 {
		fundingRate = funding / fundingWeight
	}
	return riskSnapshot{Symbol: symbol.Symbol, ObservedAt: time.Now().UTC().Truncate(time.Minute), MarkPrice: mark, SpotPrice: spot, OIUSD: oi, FundingRate: fundingRate, CVDDeltaUSD: cvd, CVDTotalUSD: total, BasisPct: (mark - spot) / spot * 100, VenueCount: venues}, true
}

func (s *riskService) binanceMetrics(ctx context.Context, symbol string, mark float64) (float64, float64) {
	if symbol == "" {
		return 0, 0
	}
	var oi struct {
		OpenInterest string `json:"openInterest"`
	}
	_ = s.getJSON(ctx, "https://fapi.binance.com/fapi/v1/openInterest?symbol="+url.QueryEscape(symbol), &oi)
	var trades []struct {
		ID         int64  `json:"a"`
		Price      string `json:"p"`
		Qty        string `json:"q"`
		BuyerMaker bool   `json:"m"`
	}
	_ = s.getJSON(ctx, "https://fapi.binance.com/fapi/v1/aggTrades?symbol="+url.QueryEscape(symbol)+"&limit=100", &trades)
	key := "binance:" + symbol
	delta := s.newTradeDelta(key, func(last int64) (int64, float64) {
		newest := last
		value := 0.0
		for _, trade := range trades {
			if trade.ID > newest {
				newest = trade.ID
			}
			if trade.ID <= last {
				continue
			}
			signed := number(trade.Price) * number(trade.Qty)
			if trade.BuyerMaker {
				signed = -signed
			}
			value += signed
		}
		return newest, value
	})
	return number(oi.OpenInterest) * mark, delta
}

func (s *riskService) okxMetrics(ctx context.Context, instrument string) (float64, float64) {
	if instrument == "" {
		return 0, 0
	}
	var funding struct {
		Data []struct {
			FundingRate string `json:"fundingRate"`
		} `json:"data"`
	}
	var trades struct {
		Data []struct {
			TradeID string `json:"tradeId"`
			Price   string `json:"px"`
			Size    string `json:"sz"`
			Side    string `json:"side"`
		} `json:"data"`
	}
	_ = s.getJSON(ctx, "https://www.okx.com/api/v5/public/funding-rate?instId="+url.QueryEscape(instrument), &funding)
	_ = s.getJSON(ctx, "https://www.okx.com/api/v5/market/trades?instId="+url.QueryEscape(instrument)+"&limit=100", &trades)
	key := "okx:" + instrument
	delta := s.newTradeDelta(key, func(last int64) (int64, float64) {
		newest := last
		value := 0.0
		for _, trade := range trades.Data {
			id, _ := strconv.ParseInt(trade.TradeID, 10, 64)
			if id > newest {
				newest = id
			}
			if id <= last {
				continue
			}
			signed := number(trade.Price) * number(trade.Size)
			if trade.Side == "sell" {
				signed = -signed
			}
			value += signed
		}
		return newest, value
	})
	rate := 0.0
	if len(funding.Data) > 0 {
		rate = number(funding.Data[0].FundingRate)
	}
	return rate, delta
}

func (s *riskService) newTradeDelta(key string, compute func(int64) (int64, float64)) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, delta := compute(s.tradeCursor[key])
	s.tradeCursor[key] = next
	return delta
}

func (s *riskService) saveSnapshot(ctx context.Context, snapshot riskSnapshot) error {
	_, err := s.store.db.ExecContext(ctx, `INSERT INTO risk_market_snapshots (symbol, observed_at, mark_price, spot_price, oi_usd, funding_rate, cvd_delta_usd, cvd_total_usd, basis_pct, venue_count) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT (symbol, observed_at) DO UPDATE SET mark_price=EXCLUDED.mark_price, spot_price=EXCLUDED.spot_price, oi_usd=EXCLUDED.oi_usd, funding_rate=EXCLUDED.funding_rate, cvd_delta_usd=EXCLUDED.cvd_delta_usd, cvd_total_usd=EXCLUDED.cvd_total_usd, basis_pct=EXCLUDED.basis_pct, venue_count=EXCLUDED.venue_count`, snapshot.Symbol, snapshot.ObservedAt, snapshot.MarkPrice, snapshot.SpotPrice, snapshot.OIUSD, snapshot.FundingRate, snapshot.CVDDeltaUSD, snapshot.CVDTotalUSD, snapshot.BasisPct, snapshot.VenueCount)
	return err
}

func (s *riskService) zones(ctx context.Context, symbol string, mark float64) ([]riskZone, error) {
	if mark <= 0 {
		return nil, nil
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT side, price, notional, occurred_at FROM liquidation_events WHERE symbol=$1 AND occurred_at >= $2`, symbol, time.Now().UTC().Add(-riskBaselineWindow))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type bucket struct {
		side         string
		price, value float64
	}
	bins := map[string]*bucket{}
	step := math.Max(mark*.0025, .00000001)
	for rows.Next() {
		var side string
		var price, notional float64
		var occurred time.Time
		if err := rows.Scan(&side, &price, &notional, &occurred); err != nil {
			return nil, err
		}
		b := math.Round(price/step) * step
		key := side + ":" + strconv.FormatFloat(b, 'f', 8, 64)
		item := bins[key]
		if item == nil {
			item = &bucket{side: side, price: b}
			bins[key] = item
		}
		item.value += notional * math.Exp(-time.Since(occurred).Hours()/4)
	}
	bySide := map[string][]riskZone{"long": {}, "short": {}}
	for _, item := range bins {
		if item.side != "long" && item.side != "short" {
			continue
		}
		bySide[item.side] = append(bySide[item.side], riskZone{Side: item.side, Price: item.price, NotionalUSD: item.value, DistancePct: math.Abs(item.price-mark) / mark * 100})
	}
	result := make([]riskZone, 0, 6)
	for _, side := range []string{"long", "short"} {
		levels := bySide[side]
		sort.Slice(levels, func(i, j int) bool { return levels[i].NotionalUSD > levels[j].NotionalUSD })
		if len(levels) > 3 {
			levels = levels[:3]
		}
		result = append(result, levels...)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].NotionalUSD > result[j].NotionalUSD })
	return result, rows.Err()
}

type externalLiquidationMap struct {
	CurrentPrice  float64     `json:"current_price"`
	GeneratedAt   int64       `json:"generated_at"`
	Prices        []float64   `json:"prices"`
	IntensityGrid [][]float64 `json:"intensity_grid"`
}

func riskForecastBaseURL() string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("RISK_ETH_LIQUIDATION_MODEL_URL")), "/")
	if base == "" {
		return "http://10.15.0.6"
	}
	return base
}

func forecastBuckets(prices []float64, grid [][]float64) []riskForecastBucket {
	result := make([]riskForecastBucket, 0, len(prices))
	for index, price := range prices {
		if price <= 0 || index >= len(grid) || len(grid[index]) == 0 {
			continue
		}
		intensity := 0.0
		for _, value := range grid[index] {
			if value > intensity && !math.IsNaN(value) && !math.IsInf(value, 0) {
				intensity = value
			}
		}
		if intensity > 0 {
			result = append(result, riskForecastBucket{Price: price, Intensity: intensity})
		}
	}
	return result
}

func topForecastBuckets(buckets []riskForecastBucket, mark float64) []riskForecastLevel {
	bySide := map[string][]riskForecastLevel{"long": {}, "short": {}}
	for _, bucket := range buckets {
		if math.Abs(bucket.Price-mark)/mark < .0005 {
			continue
		}
		side := "long"
		if bucket.Price > mark {
			side = "short"
		}
		bySide[side] = append(bySide[side], riskForecastLevel{Side: side, Price: bucket.Price, Intensity: bucket.Intensity, DistancePct: math.Abs(bucket.Price-mark) / mark * 100})
	}
	result := make([]riskForecastLevel, 0, 6)
	for _, side := range []string{"long", "short"} {
		levels := bySide[side]
		sort.Slice(levels, func(i, j int) bool { return levels[i].Intensity > levels[j].Intensity })
		if len(levels) > 3 {
			levels = levels[:3]
		}
		result = append(result, levels...)
	}
	return result
}

func topForecastLevels(prices []float64, grid [][]float64, mark float64) []riskForecastLevel {
	return topForecastBuckets(forecastBuckets(prices, grid), mark)
}

func (s *riskService) forecast(ctx context.Context, mark float64) riskForecast {
	if mark <= 0 {
		return riskForecast{Status: "unavailable", Source: "ETH 外部模型预测压力区 / 30 天窗口"}
	}
	s.forecastMu.Lock()
	defer s.forecastMu.Unlock()
	if time.Since(s.forecastAt) >= riskForecastCacheTTL {
		s.forecastAt = time.Now().UTC()
		var payload externalLiquidationMap
		endpoint := riskForecastBaseURL() + "/api/model/liquidation-map?days=" + strconv.Itoa(riskForecastWindow) + "&bucket_min=5&price_step=5&price_range=400"
		if err := s.getJSON(ctx, endpoint, &payload); err != nil || len(payload.Prices) == 0 || len(payload.IntensityGrid) == 0 {
			if err != nil {
				s.forecastErr = "外部模型预测源暂不可用"
			} else {
				s.forecastErr = "外部模型预测源未返回有效网格"
			}
		} else {
			s.forecastData = forecastBuckets(payload.Prices, payload.IntensityGrid)
			s.forecastTime = time.UnixMilli(payload.GeneratedAt).UTC()
			if payload.GeneratedAt <= 0 {
				s.forecastTime = s.forecastAt
			}
			s.forecastErr = ""
		}
	}
	if s.forecastErr != "" && len(s.forecastData) == 0 {
		return riskForecast{Status: "unavailable", Source: "ETH 外部模型预测压力区 / 30 天窗口"}
	}
	// Recalculate the top directional levels from the complete cached grid and
	// the current risk snapshot, not from the source's older model price.
	levels := topForecastBuckets(s.forecastData, mark)
	updated := s.forecastTime
	status := "ok"
	if s.forecastErr != "" {
		status = "stale"
	}
	return riskForecast{Levels: levels, Status: status, UpdatedAt: &updated, Source: "ETH 外部模型预测压力区 / 30 天窗口"}
}

func meanStd(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(len(values))
	variance := 0.0
	for _, v := range values {
		variance += (v - mean) * (v - mean)
	}
	return mean, math.Sqrt(variance / float64(len(values)))
}
func zScore(value float64, values []float64) float64 {
	mean, std := meanStd(values)
	if std < 0.000000001 {
		return 0
	}
	return (value - mean) / std
}

func (s *riskService) history(ctx context.Context, symbol string) ([]riskSnapshot, error) {
	rows, err := s.store.db.QueryContext(ctx, `SELECT symbol, observed_at, mark_price, spot_price, oi_usd, funding_rate, cvd_delta_usd, cvd_total_usd, basis_pct, venue_count FROM risk_market_snapshots WHERE symbol=$1 AND observed_at >= $2 ORDER BY observed_at DESC`, symbol, time.Now().UTC().Add(-riskBaselineWindow))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []riskSnapshot{}
	for rows.Next() {
		var item riskSnapshot
		if err := rows.Scan(&item.Symbol, &item.ObservedAt, &item.MarkPrice, &item.SpotPrice, &item.OIUSD, &item.FundingRate, &item.CVDDeltaUSD, &item.CVDTotalUSD, &item.BasisPct, &item.VenueCount); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *riskService) signals(ctx context.Context, snapshot riskSnapshot, zones []riskZone) ([]riskSignal, error) {
	history, err := s.history(ctx, snapshot.Symbol)
	if err != nil {
		return nil, err
	}
	oiValues, fundingValues, cvdValues, basisValues := []float64{}, []float64{}, []float64{}, []float64{}
	for _, item := range history {
		if !item.ObservedAt.Equal(snapshot.ObservedAt) {
			oiValues = append(oiValues, item.OIUSD)
			fundingValues = append(fundingValues, item.FundingRate)
			cvdValues = append(cvdValues, item.CVDDeltaUSD)
			basisValues = append(basisValues, item.BasisPct)
		}
	}
	activeBaseline := len(oiValues) >= 6
	oiZ, fundingZ, cvdZ, basisZ := zScore(snapshot.OIUSD, oiValues), zScore(snapshot.FundingRate, fundingValues), zScore(snapshot.CVDDeltaUSD, cvdValues), zScore(snapshot.BasisPct, basisValues)
	previous := riskZone{}
	s.mu.RLock()
	previous = s.previousZone[snapshot.Symbol]
	s.mu.RUnlock()
	nearest := riskZone{}
	if len(zones) > 0 {
		nearest = zones[0]
		for _, zone := range zones {
			if zone.DistancePct < nearest.DistancePct {
				nearest = zone
			}
		}
	}
	structure := activeBaseline && previous.Price > 0 && nearest.Price > 0 && nearest.DistancePct < previous.DistancePct*.75 && nearest.NotionalUSD > previous.NotionalUSD*1.2
	oiFunding := activeBaseline && math.Abs(oiZ) >= 2 && math.Abs(fundingZ) >= 1.5 && snapshot.FundingRate*fundingZ > 0
	priceChange := 0.0
	if len(history) > 0 && history[0].MarkPrice > 0 {
		priceChange = (snapshot.MarkPrice - history[0].MarkPrice) / history[0].MarkPrice
	}
	cvd := activeBaseline && math.Abs(cvdZ) >= 2 && (snapshot.CVDDeltaUSD*priceChange) < 0
	basis := activeBaseline && math.Abs(basisZ) >= 2
	near := nearest.Price > 0 && nearest.DistancePct <= 1
	zoneDetail := "暂无足够的实际强平事件"
	if nearest.Price > 0 {
		zoneDetail = fmt.Sprintf("最近%s观测热区 %.6g，距离现价 %.2f%%", nearest.Side, nearest.Price, nearest.DistancePct)
	}
	return []riskSignal{{ID: "structure", Title: "清算结构迁移", Detail: "实际强平价格桶向现价迁移且密度增强", Active: structure, Severity: "high"}, {ID: "oi_funding", Title: "OI / Funding 异常", Detail: fmt.Sprintf("OI 偏离 %.1fσ，Funding 偏离 %.1fσ", oiZ, fundingZ), Active: oiFunding, Severity: "high"}, {ID: "cvd", Title: "CVD 背离", Detail: fmt.Sprintf("5 分钟主动成交累计偏离 %.1fσ", cvdZ), Active: cvd, Severity: "medium"}, {ID: "basis", Title: "现货 / 衍生品价差背离", Detail: fmt.Sprintf("永续相对现货基差 %.3f%%，偏离 %.1fσ", snapshot.BasisPct, basisZ), Active: basis, Severity: "medium"}, {ID: "near_zone", Title: "价格接近清算区", Detail: zoneDetail, Active: near, Severity: "medium"}}, nil
}

func riskLevel(count int) string {
	if count >= 4 {
		return "CRITICAL"
	}
	if count == 3 {
		return "HIGH"
	}
	if count == 2 {
		return "MEDIUM"
	}
	return "OBSERVE"
}

func (s *riskService) evaluateAll(ctx context.Context) {
	if s.store == nil {
		return
	}
	s.mu.RLock()
	snapshots := make([]riskSnapshot, 0, len(s.latest))
	for _, item := range s.latest {
		snapshots = append(snapshots, item)
	}
	s.mu.RUnlock()
	for _, snapshot := range snapshots {
		zones, err := s.zones(ctx, snapshot.Symbol, snapshot.MarkPrice)
		if err != nil {
			continue
		}
		signals, err := s.signals(ctx, snapshot, zones)
		if err != nil {
			continue
		}
		count := 0
		for _, signal := range signals {
			if signal.Active {
				count++
			}
		}
		if count < 2 {
			continue
		}
		s.createAlert(ctx, snapshot, signals, count)
	}
}

func (s *riskService) createAlert(ctx context.Context, snapshot riskSnapshot, signals []riskSignal, count int) {
	level := riskLevel(count)
	var last time.Time
	var lastLevel string
	_ = s.store.db.QueryRowContext(ctx, `SELECT created_at, level FROM risk_alert_events WHERE symbol=$1 ORDER BY created_at DESC LIMIT 1`, snapshot.Symbol).Scan(&last, &lastLevel)
	if !last.IsZero() && time.Since(last) < riskAlertCooldown && level == lastLevel {
		return
	}
	signalsJSON, _ := json.Marshal(signals)
	snapshotJSON, _ := json.Marshal(snapshot)
	var id int64
	err := s.store.db.QueryRowContext(ctx, `INSERT INTO risk_alert_events (symbol,level,trigger_count,signals,snapshot) VALUES ($1,$2,$3,$4,$5) RETURNING id`, snapshot.Symbol, level, count, signalsJSON, snapshotJSON).Scan(&id)
	if err != nil {
		return
	}
	status, detail := s.sendTelegram(ctx, snapshot, signals, level)
	_, _ = s.store.db.ExecContext(ctx, `UPDATE risk_alert_events SET telegram_status=$1, telegram_error=$2 WHERE id=$3`, status, detail, id)
}

func (s *riskService) sendTelegram(ctx context.Context, snapshot riskSnapshot, signals []riskSignal, level string) (string, string) {
	token, chat := strings.TrimSpace(os.Getenv("RISK_TELEGRAM_BOT_TOKEN")), strings.TrimSpace(os.Getenv("RISK_TELEGRAM_CHAT_ID"))
	if token == "" || chat == "" {
		return "not_configured", ""
	}
	active := []string{}
	for _, signal := range signals {
		if signal.Active {
			active = append(active, signal.Title)
		}
	}
	body, _ := json.Marshal(map[string]string{"chat_id": chat, "text": fmt.Sprintf("风险雷达 %s\n%s · %s\n触发：%s\n价格：%.6g · OI：$%.2fM · 基差：%.3f%%\n研究提示，不构成交易建议。", level, snapshot.Symbol, snapshot.ObservedAt.Local().Format("01-02 15:04"), strings.Join(active, "、"), snapshot.MarkPrice, snapshot.OIUSD/1e6, snapshot.BasisPct)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+token+"/sendMessage", strings.NewReader(string(body)))
	if err != nil {
		return "failed", "request creation failed"
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(req)
	if err != nil {
		return "failed", "delivery request failed"
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "failed", fmt.Sprintf("telegram HTTP %d", response.StatusCode)
	}
	return "sent", ""
}

func (s *riskService) latestSnapshot(ctx context.Context, symbol string) (riskSnapshot, error) {
	var item riskSnapshot
	err := s.store.db.QueryRowContext(ctx, `SELECT symbol, observed_at, mark_price, spot_price, oi_usd, funding_rate, cvd_delta_usd, cvd_total_usd, basis_pct, venue_count FROM risk_market_snapshots WHERE symbol=$1 ORDER BY observed_at DESC LIMIT 1`, symbol).Scan(&item.Symbol, &item.ObservedAt, &item.MarkPrice, &item.SpotPrice, &item.OIUSD, &item.FundingRate, &item.CVDDeltaUSD, &item.CVDTotalUSD, &item.BasisPct, &item.VenueCount)
	return item, err
}

func (s *riskService) historyForRange(ctx context.Context, symbol string, duration time.Duration) ([]riskSnapshot, error) {
	rows, err := s.store.db.QueryContext(ctx, `SELECT symbol, observed_at, mark_price, spot_price, oi_usd, funding_rate, cvd_delta_usd, cvd_total_usd, basis_pct, venue_count FROM risk_market_snapshots WHERE symbol=$1 AND observed_at >= $2 ORDER BY observed_at ASC`, symbol, time.Now().UTC().Add(-duration))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []riskSnapshot{}
	for rows.Next() {
		var item riskSnapshot
		if err := rows.Scan(&item.Symbol, &item.ObservedAt, &item.MarkPrice, &item.SpotPrice, &item.OIUSD, &item.FundingRate, &item.CVDDeltaUSD, &item.CVDTotalUSD, &item.BasisPct, &item.VenueCount); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func riskHistoryRange(value string) (string, time.Duration, bool) {
	if value == "" {
		value = "24h"
	}
	durations := map[string]time.Duration{"4h": 4 * time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour}
	duration, valid := durations[value]
	return value, duration, valid
}

func (s *riskService) eventList(ctx context.Context, symbol string, limit int) ([]riskAlertEvent, error) {
	query := `SELECT id,symbol,level,trigger_count,signals,snapshot,created_at,telegram_status,telegram_error FROM risk_alert_events`
	args := []any{}
	if symbol != "" {
		query += " WHERE symbol=$1"
		args = append(args, symbol)
	}
	query += " ORDER BY created_at DESC LIMIT " + strconv.Itoa(limit)
	rows, err := s.store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []riskAlertEvent{}
	for rows.Next() {
		var item riskAlertEvent
		var signals, snapshot []byte
		if err := rows.Scan(&item.ID, &item.Symbol, &item.Level, &item.TriggerCount, &signals, &snapshot, &item.CreatedAt, &item.TelegramStatus, &item.TelegramError); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(signals, &item.Signals)
		_ = json.Unmarshal(snapshot, &item.Snapshot)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *riskService) serveSymbols(w http.ResponseWriter, _ *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "risk radar database is not configured"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"symbols": []marketSymbol{{Symbol: riskETHSymbol}}, "status": "collecting"})
}
func (s *riskService) serveSnapshot(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "risk radar database is not configured"})
		return
	}
	symbol := strings.TrimSpace(r.URL.Query().Get("symbol"))
	if symbol == "" {
		symbol = riskETHSymbol
	}
	if symbol != riskETHSymbol {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ETH-USDT is the only supported risk radar symbol"})
		return
	}
	snapshot, err := s.latestSnapshot(r.Context(), symbol)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "risk radar is collecting its first snapshot"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to load risk snapshot"})
		return
	}
	zones, _ := s.zones(r.Context(), symbol, snapshot.MarkPrice)
	signals, _ := s.signals(r.Context(), snapshot, zones)
	events, _ := s.eventList(r.Context(), symbol, 8)
	writeJSON(w, http.StatusOK, riskRadarResponse{Snapshot: snapshot, Zones: zones, ObservedLevels: zones, Forecast: s.forecast(r.Context(), snapshot.MarkPrice), Signals: signals, Events: events, Status: "ok"})
}
func (s *riskService) serveHistory(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "risk radar database is not configured"})
		return
	}
	rangeName, duration, valid := riskHistoryRange(strings.TrimSpace(r.URL.Query().Get("range")))
	if !valid {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "range must be one of 4h, 24h, 7d"})
		return
	}
	snapshots, err := s.historyForRange(r.Context(), riskETHSymbol, duration)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to load risk history"})
		return
	}
	status := "ok"
	if len(snapshots) == 0 {
		status = "collecting"
	}
	writeJSON(w, http.StatusOK, riskHistoryResponse{Symbol: riskETHSymbol, Range: rangeName, Snapshots: snapshots, Status: status})
}
func (s *riskService) serveEvents(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "risk radar database is not configured"})
		return
	}
	events, err := s.eventList(r.Context(), riskETHSymbol, 50)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to load risk events"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}
