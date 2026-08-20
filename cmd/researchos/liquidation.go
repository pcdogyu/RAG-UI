package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	liquidationRetention = 7 * 24 * time.Hour
	defaultSymbolLimit   = 50
	fallbackPollInterval = 5 * time.Second
	fallbackBackfillAge  = 24 * time.Hour
	defaultFallbackURL   = "http://10.15.0.6"
)

type liquidationEvent struct {
	Exchange string    `json:"exchange"`
	Symbol   string    `json:"symbol"`
	Occurred time.Time `json:"occurredAt"`
	Side     string    `json:"side"`
	Price    float64   `json:"price"`
	Quantity float64   `json:"quantity"`
	Notional float64   `json:"notional"`
	DedupKey string    `json:"-"`
}

type candle struct {
	Symbol   string    `json:"symbol"`
	Interval string    `json:"interval"`
	OpenTime time.Time `json:"openTime"`
	Open     float64   `json:"open"`
	High     float64   `json:"high"`
	Low      float64   `json:"low"`
	Close    float64   `json:"close"`
	Volume   float64   `json:"volume"`
}

type marketSymbol struct {
	Symbol           string  `json:"symbol"`
	BinanceSymbol    string  `json:"-"`
	OKXInstrumentID  string  `json:"-"`
	OKXContractValue float64 `json:"-"`
	Turnover         float64 `json:"turnover"`
}

type exchangeStatus struct {
	Connected         bool      `json:"connected"`
	LastEvent         time.Time `json:"lastEvent,omitempty"`
	LastDirectEvent   time.Time `json:"lastDirectEvent,omitempty"`
	LastFallbackEvent time.Time `json:"lastFallbackEvent,omitempty"`
	LastMessage       time.Time `json:"lastMessage,omitempty"`
	Error             string    `json:"error,omitempty"`
}

type fallbackStatus struct {
	Enabled     bool      `json:"enabled"`
	LastSuccess time.Time `json:"lastSuccess,omitempty"`
	Error       string    `json:"error,omitempty"`
}

type liquidationStatus struct {
	Database  bool                      `json:"database"`
	Exchanges map[string]exchangeStatus `json:"exchanges"`
	Fallback  fallbackStatus            `json:"fallback"`
}

type liquidationStore struct{ db *sql.DB }

func openLiquidationStore(ctx context.Context, dsn string) (*liquidationStore, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, nil
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	store := &liquidationStore{db: db}
	if err := store.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *liquidationStore) Close() error { return s.db.Close() }

func (s *liquidationStore) migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS liquidation_events (
			dedup_key TEXT PRIMARY KEY, exchange TEXT NOT NULL, symbol TEXT NOT NULL,
			occurred_at TIMESTAMPTZ NOT NULL, side TEXT NOT NULL,
			price DOUBLE PRECISION NOT NULL, quantity DOUBLE PRECISION NOT NULL,
			notional DOUBLE PRECISION NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX IF NOT EXISTS liquidation_events_symbol_time_idx ON liquidation_events (symbol, occurred_at DESC)`,
		`CREATE TABLE IF NOT EXISTS liquidation_candles (
			symbol TEXT NOT NULL, interval TEXT NOT NULL, open_time TIMESTAMPTZ NOT NULL,
			open DOUBLE PRECISION NOT NULL, high DOUBLE PRECISION NOT NULL, low DOUBLE PRECISION NOT NULL,
			close DOUBLE PRECISION NOT NULL, volume DOUBLE PRECISION NOT NULL,
			PRIMARY KEY (symbol, interval, open_time)
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (s *liquidationStore) saveEvent(ctx context.Context, event liquidationEvent) (bool, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO liquidation_events
		(dedup_key, exchange, symbol, occurred_at, side, price, quantity, notional)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (dedup_key) DO NOTHING`,
		event.DedupKey, event.Exchange, event.Symbol, event.Occurred, event.Side, event.Price, event.Quantity, event.Notional)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *liquidationStore) saveCandle(ctx context.Context, item candle) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO liquidation_candles
		(symbol, interval, open_time, open, high, low, close, volume)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (symbol, interval, open_time) DO UPDATE SET open=EXCLUDED.open, high=EXCLUDED.high,
		low=EXCLUDED.low, close=EXCLUDED.close, volume=EXCLUDED.volume`,
		item.Symbol, item.Interval, item.OpenTime, item.Open, item.High, item.Low, item.Close, item.Volume)
	return err
}

func (s *liquidationStore) chart(ctx context.Context, symbol string, since time.Time, exchanges map[string]bool) ([]candle, []liquidationEvent, time.Time, error) {
	candleRows, err := s.db.QueryContext(ctx, `SELECT symbol, interval, open_time, open, high, low, close, volume
		FROM liquidation_candles WHERE symbol=$1 AND interval='5m' AND open_time >= $2 ORDER BY open_time`, symbol, since)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	defer candleRows.Close()
	candles := make([]candle, 0)
	for candleRows.Next() {
		var item candle
		if err := candleRows.Scan(&item.Symbol, &item.Interval, &item.OpenTime, &item.Open, &item.High, &item.Low, &item.Close, &item.Volume); err != nil {
			return nil, nil, time.Time{}, err
		}
		candles = append(candles, item)
	}
	if err := candleRows.Err(); err != nil {
		return nil, nil, time.Time{}, err
	}

	query := `SELECT exchange, symbol, occurred_at, side, price, quantity, notional FROM liquidation_events WHERE symbol=$1 AND occurred_at >= $2`
	args := []any{symbol, since}
	if len(exchanges) == 1 {
		for exchange := range exchanges {
			query += ` AND exchange=$3`
			args = append(args, exchange)
		}
	}
	query += ` ORDER BY occurred_at`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	defer rows.Close()
	events := make([]liquidationEvent, 0)
	for rows.Next() {
		var item liquidationEvent
		if err := rows.Scan(&item.Exchange, &item.Symbol, &item.Occurred, &item.Side, &item.Price, &item.Quantity, &item.Notional); err != nil {
			return nil, nil, time.Time{}, err
		}
		events = append(events, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, time.Time{}, err
	}
	var collectionStart sql.NullTime
	if err := s.db.QueryRowContext(ctx, `SELECT min(occurred_at) FROM liquidation_events`).Scan(&collectionStart); err != nil {
		return nil, nil, time.Time{}, err
	}
	return candles, events, collectionStart.Time, nil
}

func (s *liquidationStore) candleCount(ctx context.Context, symbol string, since time.Time) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM liquidation_candles WHERE symbol=$1 AND interval='5m' AND open_time >= $2`, symbol, since).Scan(&count)
	return count, err
}

func (s *liquidationStore) cleanup(ctx context.Context, retention time.Duration) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM liquidation_events WHERE occurred_at < $1`, time.Now().UTC().Add(-retention))
	return err
}

type liquidationSubscriber struct {
	symbol    string
	exchanges map[string]bool
	updates   chan liquidationEvent
}

type liquidationService struct {
	store       *liquidationStore
	client      *http.Client
	fallbackURL string
	mu          sync.RWMutex
	symbols     []marketSymbol
	byBinance   map[string]marketSymbol
	byOKX       map[string]marketSymbol
	statuses    map[string]exchangeStatus
	fallback    fallbackStatus
	subscribers map[*liquidationSubscriber]struct{}
}

func newLiquidationService(store *liquidationStore) *liquidationService {
	service := &liquidationService{
		store: store, client: &http.Client{Timeout: 15 * time.Second}, fallbackURL: fallbackURL(),
		byBinance: map[string]marketSymbol{}, byOKX: map[string]marketSymbol{},
		statuses: map[string]exchangeStatus{"binance": {}, "okx": {}}, fallback: fallbackStatus{Enabled: true}, subscribers: map[*liquidationSubscriber]struct{}{},
	}
	// ETH is immediately usable while the first public instrument scan runs.
	seed := marketSymbol{Symbol: "ETH-USDT", BinanceSymbol: "ETHUSDT", OKXInstrumentID: "ETH-USDT-SWAP", OKXContractValue: 0.01}
	service.symbols = []marketSymbol{seed}
	service.byBinance[seed.BinanceSymbol] = seed
	service.byOKX[seed.OKXInstrumentID] = seed
	return service
}

func (s *liquidationService) start(ctx context.Context) {
	go s.universeLoop(ctx)
	go s.binanceLoop(ctx)
	go s.okxLoop(ctx)
	if s.store != nil {
		go s.fallbackLoop(ctx)
		retention := retentionDuration()
		go func() {
			ticker := time.NewTicker(time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := s.store.cleanup(ctx, retention); err != nil {
						log.Printf("liquidation cleanup: %v", err)
					}
				}
			}
		}()
	}
}

func (s *liquidationService) universeLoop(ctx context.Context) {
	for {
		if err := s.refreshUniverse(ctx); err != nil {
			log.Printf("liquidation universe: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Hour):
		}
	}
}

func (s *liquidationService) symbolsSnapshot() []marketSymbol {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := append([]marketSymbol(nil), s.symbols...)
	return result
}

func (s *liquidationService) statusSnapshot() liquidationStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	statuses := map[string]exchangeStatus{}
	for key, value := range s.statuses {
		statuses[key] = value
	}
	return liquidationStatus{Database: s.store != nil, Exchanges: statuses, Fallback: s.fallback}
}

func (s *liquidationService) setDirectStatus(exchange string, connected bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.statuses[exchange]
	status.Connected = connected
	if err != nil {
		status.Error = err.Error()
	} else if connected {
		status.Error = ""
	}
	s.statuses[exchange] = status
}

func (s *liquidationService) markDirectMessage(exchange string) {
	s.mu.Lock()
	status := s.statuses[exchange]
	status.LastMessage = time.Now().UTC()
	s.statuses[exchange] = status
	s.mu.Unlock()
}

func (s *liquidationService) recordDirectParseError(exchange string, err error) {
	s.mu.Lock()
	status := s.statuses[exchange]
	status.Error = err.Error()
	s.statuses[exchange] = status
	s.mu.Unlock()
}

func (s *liquidationService) setFallbackStatus(success bool, err error) {
	s.mu.Lock()
	if success {
		s.fallback.LastSuccess = time.Now().UTC()
		s.fallback.Error = ""
	} else if err != nil {
		s.fallback.Error = err.Error()
	}
	s.mu.Unlock()
}

func (s *liquidationService) refreshUniverse(ctx context.Context) error {
	binance, err := s.binanceUniverse(ctx)
	if err != nil {
		return fmt.Errorf("binance universe: %w", err)
	}
	okx, err := s.okxUniverse(ctx)
	if err != nil {
		return fmt.Errorf("okx universe: %w", err)
	}
	combined := make([]marketSymbol, 0)
	for base, b := range binance {
		o, exists := okx[base]
		if !exists {
			continue
		}
		combined = append(combined, marketSymbol{Symbol: base + "-USDT", BinanceSymbol: b.Symbol, OKXInstrumentID: o.Symbol, OKXContractValue: o.ContractValue, Turnover: b.Turnover + o.Turnover})
	}
	sort.Slice(combined, func(i, j int) bool { return combined[i].Turnover > combined[j].Turnover })
	limit := defaultSymbolLimit
	if raw := strings.TrimSpace(os.Getenv("LIQUIDATION_SYMBOL_LIMIT")); raw != "" {
		if value, convErr := strconv.Atoi(raw); convErr == nil && value > 0 && value <= 100 {
			limit = value
		}
	}
	if len(combined) > limit {
		combined = combined[:limit]
	}
	if len(combined) == 0 {
		return fmt.Errorf("no common USDT perpetuals returned")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.symbols, s.byBinance, s.byOKX = combined, map[string]marketSymbol{}, map[string]marketSymbol{}
	for _, symbol := range combined {
		s.byBinance[symbol.BinanceSymbol] = symbol
		s.byOKX[symbol.OKXInstrumentID] = symbol
	}
	return nil
}

type venueInstrument struct {
	Symbol        string
	Turnover      float64
	ContractValue float64
}

func (s *liquidationService) binanceUniverse(ctx context.Context) (map[string]venueInstrument, error) {
	var exchangeInfo struct {
		Symbols []struct {
			Symbol       string `json:"symbol"`
			ContractType string `json:"contractType"`
			QuoteAsset   string `json:"quoteAsset"`
			Status       string `json:"status"`
			BaseAsset    string `json:"baseAsset"`
		} `json:"symbols"`
	}
	if err := s.getJSON(ctx, "https://fapi.binance.com/fapi/v1/exchangeInfo", &exchangeInfo); err != nil {
		return nil, err
	}
	var tickers []struct {
		Symbol      string `json:"symbol"`
		QuoteVolume string `json:"quoteVolume"`
	}
	if err := s.getJSON(ctx, "https://fapi.binance.com/fapi/v1/ticker/24hr", &tickers); err != nil {
		return nil, err
	}
	volume := map[string]float64{}
	for _, ticker := range tickers {
		volume[ticker.Symbol] = number(ticker.QuoteVolume)
	}
	result := map[string]venueInstrument{}
	for _, item := range exchangeInfo.Symbols {
		if item.ContractType == "PERPETUAL" && item.QuoteAsset == "USDT" && item.Status == "TRADING" {
			result[item.BaseAsset] = venueInstrument{Symbol: item.Symbol, Turnover: volume[item.Symbol]}
		}
	}
	return result, nil
}

func (s *liquidationService) okxUniverse(ctx context.Context) (map[string]venueInstrument, error) {
	var instruments struct {
		Data []struct {
			InstID    string `json:"instId"`
			BaseCcy   string `json:"baseCcy"`
			SettleCcy string `json:"settleCcy"`
			State     string `json:"state"`
			CtVal     string `json:"ctVal"`
		} `json:"data"`
	}
	if err := s.getJSON(ctx, "https://www.okx.com/api/v5/public/instruments?instType=SWAP", &instruments); err != nil {
		return nil, err
	}
	var tickers struct {
		Data []struct {
			InstID         string `json:"instId"`
			VolCcyQuote24h string `json:"volCcyQuote24h"`
		} `json:"data"`
	}
	if err := s.getJSON(ctx, "https://www.okx.com/api/v5/market/tickers?instType=SWAP", &tickers); err != nil {
		return nil, err
	}
	volume := map[string]float64{}
	for _, ticker := range tickers.Data {
		volume[ticker.InstID] = number(ticker.VolCcyQuote24h)
	}
	result := map[string]venueInstrument{}
	for _, item := range instruments.Data {
		if item.SettleCcy != "USDT" || item.State != "live" || !strings.HasSuffix(item.InstID, "-USDT-SWAP") {
			continue
		}
		base := item.BaseCcy
		if base == "" {
			base = strings.TrimSuffix(item.InstID, "-USDT-SWAP")
		}
		result[base] = venueInstrument{Symbol: item.InstID, Turnover: volume[item.InstID], ContractValue: number(item.CtVal)}
	}
	return result, nil
}

func (s *liquidationService) getJSON(ctx context.Context, endpoint string, result any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("HTTP %s", response.Status)
	}
	return json.NewDecoder(response.Body).Decode(result)
}

type fallbackLiquidationRow struct {
	Exchange string  `json:"exchange"`
	Symbol   string  `json:"symbol"`
	Side     string  `json:"side"`
	Price    float64 `json:"price"`
	Quantity float64 `json:"qty"`
	Notional float64 `json:"notional_usd"`
	EventTS  int64   `json:"event_ts"`
}

type fallbackLiquidationResponse struct {
	Rows []fallbackLiquidationRow `json:"rows"`
}

func fallbackURL() string {
	if value := strings.TrimRight(strings.TrimSpace(os.Getenv("LIQUIDATION_FALLBACK_URL")), "/"); value != "" {
		return value
	}
	return defaultFallbackURL
}

func (s *liquidationService) fallbackLoop(ctx context.Context) {
	if err := s.refreshUniverse(ctx); err != nil {
		log.Printf("liquidation fallback universe: %v", err)
	}
	since := time.Now().UTC().Add(-fallbackBackfillAge)
	if err := s.syncFallback(ctx, since); err != nil {
		s.setFallbackStatus(false, err)
		log.Printf("liquidation fallback backfill: %v", err)
	}
	ticker := time.NewTicker(fallbackPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.syncFallback(ctx, time.Now().UTC().Add(-2*time.Minute)); err != nil {
				s.setFallbackStatus(false, err)
				log.Printf("liquidation fallback poll: %v", err)
			}
		}
	}
}

func (s *liquidationService) syncFallback(ctx context.Context, since time.Time) error {
	endpoint := s.fallbackURL + "/api/liquidations"
	for page := 1; page <= 200; page++ {
		query := url.Values{"symbol": {"ALL"}, "page": {strconv.Itoa(page)}, "limit": {"1000"}, "filter_field": {"notional_usd"}, "min_value": {"0"}}
		var response fallbackLiquidationResponse
		if err := s.getJSON(ctx, endpoint+"?"+query.Encode(), &response); err != nil {
			return err
		}
		if len(response.Rows) == 0 {
			s.setFallbackStatus(true, nil)
			return nil
		}
		oldest := time.Now().UTC()
		for _, row := range response.Rows {
			occurred := time.UnixMilli(row.EventTS).UTC()
			if occurred.Before(oldest) {
				oldest = occurred
			}
			if occurred.Before(since) {
				continue
			}
			if event, ok := s.normalizeFallback(row); ok {
				s.handleEvent(ctx, event, "fallback")
			}
		}
		if oldest.Before(since) || oldest.Equal(since) || len(response.Rows) < 1000 {
			s.setFallbackStatus(true, nil)
			return nil
		}
	}
	return fmt.Errorf("fallback page limit reached before %s", since.Format(time.RFC3339))
}

func (s *liquidationService) normalizeFallback(row fallbackLiquidationRow) (liquidationEvent, bool) {
	exchange := strings.ToLower(strings.TrimSpace(row.Exchange))
	if exchange != "binance" && exchange != "okx" {
		return liquidationEvent{}, false
	}
	rawSymbol := strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(row.Symbol)), "-", "")
	s.mu.RLock()
	symbol, exists := s.byBinance[rawSymbol]
	s.mu.RUnlock()
	side := strings.ToLower(strings.TrimSpace(row.Side))
	if !exists || (side != "long" && side != "short") || row.Price <= 0 || row.Quantity <= 0 || row.EventTS <= 0 {
		return liquidationEvent{}, false
	}
	occurred := time.UnixMilli(row.EventTS).UTC()
	notional := row.Notional
	if notional <= 0 {
		notional = row.Price * row.Quantity
	}
	return liquidationEvent{Exchange: exchange, Symbol: symbol.Symbol, Occurred: occurred, Side: side, Price: row.Price, Quantity: row.Quantity, Notional: notional, DedupKey: liquidationDedupKey(exchange, symbol.Symbol, occurred, row.Price, row.Quantity)}, true
}

// backfillCandles fills price history from OKX; liquidation history is backfilled separately
// from the designated fallback collector with its original exchange timestamps preserved.
func (s *liquidationService) backfillCandles(ctx context.Context, canonicalSymbol string, since time.Time) error {
	if s.store == nil {
		return nil
	}
	s.mu.RLock()
	var instrument string
	for _, symbol := range s.symbols {
		if symbol.Symbol == canonicalSymbol {
			instrument = symbol.OKXInstrumentID
			break
		}
	}
	s.mu.RUnlock()
	if instrument == "" {
		return fmt.Errorf("unknown symbol %s", canonicalSymbol)
	}
	after := ""
	for page := 0; page < 12; page++ {
		endpoint := "https://www.okx.com/api/v5/market/history-candles?instId=" + url.QueryEscape(instrument) + "&bar=5m&limit=300"
		if after != "" {
			endpoint += "&after=" + url.QueryEscape(after)
		}
		var response struct {
			Data [][]string `json:"data"`
		}
		if err := s.getJSON(ctx, endpoint, &response); err != nil {
			return err
		}
		if len(response.Data) == 0 {
			return nil
		}
		oldest := time.Now().UTC()
		for _, row := range response.Data {
			if len(row) < 6 {
				continue
			}
			timestamp, err := strconv.ParseInt(row[0], 10, 64)
			if err != nil {
				continue
			}
			openTime := time.UnixMilli(timestamp).UTC()
			if openTime.Before(oldest) {
				oldest = openTime
			}
			if err := s.store.saveCandle(ctx, candle{Symbol: canonicalSymbol, Interval: "5m", OpenTime: openTime, Open: number(row[1]), High: number(row[2]), Low: number(row[3]), Close: number(row[4]), Volume: number(row[5])}); err != nil {
				return err
			}
		}
		if oldest.Before(since) || oldest.Equal(since) {
			return nil
		}
		after = strconv.FormatInt(oldest.UnixMilli(), 10)
	}
	return nil
}

func (s *liquidationService) binanceLoop(ctx context.Context) {
	for ctx.Err() == nil {
		if len(s.symbolsSnapshot()) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}
		err := s.runBinance(ctx)
		s.setDirectStatus("binance", false, err)
		if err != nil {
			log.Printf("binance liquidation stream: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func (s *liquidationService) runBinance(ctx context.Context) error {
	conn, _, err := websocket.Dial(ctx, "wss://fstream.binance.com/ws", nil)
	if err != nil {
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := wsjson.Write(ctx, conn, map[string]any{"method": "SUBSCRIBE", "params": []string{"!forceOrder@arr"}, "id": 1}); err != nil {
		return err
	}
	s.setDirectStatus("binance", true, nil)
	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			return err
		}
		s.markDirectMessage("binance")
		for _, event := range s.normalizeBinanceForceOrders(raw) {
			s.handleEvent(ctx, event, "direct")
		}
	}
}

type binanceForceOrder struct {
	Order struct {
		Symbol         string `json:"s"`
		Side           string `json:"S"`
		AveragePrice   string `json:"ap"`
		FilledQuantity string `json:"z"`
		TradeTime      int64  `json:"T"`
	} `json:"o"`
	Data json.RawMessage `json:"data"`
}

func (s *liquidationService) normalizeBinanceForceOrders(raw json.RawMessage) []liquidationEvent {
	var batch []binanceForceOrder
	if len(raw) > 0 && raw[0] == '[' {
		if err := json.Unmarshal(raw, &batch); err != nil {
			s.recordDirectParseError("binance", err)
			log.Printf("decode binance liquidation batch: %v", err)
			return nil
		}
	} else {
		var item binanceForceOrder
		if err := json.Unmarshal(raw, &item); err != nil {
			s.recordDirectParseError("binance", err)
			log.Printf("decode binance liquidation message: %v", err)
			return nil
		}
		if len(item.Data) > 0 {
			return s.normalizeBinanceForceOrders(item.Data)
		}
		batch = []binanceForceOrder{item}
	}
	events := make([]liquidationEvent, 0, len(batch))
	for _, item := range batch {
		if event, ok := s.normalizeBinance(item.Order.Symbol, item.Order.Side, item.Order.AveragePrice, item.Order.FilledQuantity, item.Order.TradeTime); ok {
			events = append(events, event)
		}
	}
	return events
}

func (s *liquidationService) okxLoop(ctx context.Context) {
	for ctx.Err() == nil {
		if len(s.symbolsSnapshot()) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}
		err := s.runOKX(ctx)
		s.setDirectStatus("okx", false, err)
		if err != nil {
			log.Printf("okx liquidation stream: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func (s *liquidationService) runOKX(ctx context.Context) error {
	conn, _, err := websocket.Dial(ctx, "wss://ws.okx.com:8443/ws/v5/business", nil)
	if err != nil {
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	args := []map[string]string{{"channel": "liquidation-orders", "instType": "SWAP"}}
	for _, symbol := range s.symbolsSnapshot() {
		args = append(args, map[string]string{"channel": "candle5m", "instId": symbol.OKXInstrumentID})
	}
	if err := wsjson.Write(ctx, conn, map[string]any{"op": "subscribe", "args": args}); err != nil {
		return err
	}
	s.setDirectStatus("okx", true, nil)
	for {
		var message struct {
			Arg struct {
				Channel string `json:"channel"`
				InstID  string `json:"instId"`
			} `json:"arg"`
			Data []json.RawMessage `json:"data"`
		}
		if err := wsjson.Read(ctx, conn, &message); err != nil {
			return err
		}
		s.markDirectMessage("okx")
		if message.Arg.Channel == "candle5m" {
			for _, raw := range message.Data {
				if item, ok := s.normalizeOKXCandle(message.Arg.InstID, raw); ok && s.store != nil {
					if err := s.store.saveCandle(ctx, item); err != nil {
						log.Printf("save candle: %v", err)
					}
				}
			}
			continue
		}
		if message.Arg.Channel == "liquidation-orders" {
			for _, raw := range message.Data {
				for _, event := range s.normalizeOKXLiquidations(raw) {
					s.handleEvent(ctx, event, "direct")
				}
			}
		}
	}
}

func (s *liquidationService) normalizeBinance(rawSymbol, orderSide, priceRaw, quantityRaw string, timestamp int64) (liquidationEvent, bool) {
	s.mu.RLock()
	symbol, exists := s.byBinance[rawSymbol]
	s.mu.RUnlock()
	price, quantity := number(priceRaw), number(quantityRaw)
	if !exists || price <= 0 || quantity <= 0 || timestamp <= 0 {
		return liquidationEvent{}, false
	}
	side := "long"
	if orderSide == "BUY" {
		side = "short"
	}
	occurred := time.UnixMilli(timestamp).UTC()
	return liquidationEvent{Exchange: "binance", Symbol: symbol.Symbol, Occurred: occurred, Side: side, Price: price, Quantity: quantity, Notional: price * quantity, DedupKey: liquidationDedupKey("binance", symbol.Symbol, occurred, price, quantity)}, true
}

func (s *liquidationService) normalizeOKXCandle(instID string, raw json.RawMessage) (candle, bool) {
	s.mu.RLock()
	symbol, exists := s.byOKX[instID]
	s.mu.RUnlock()
	if !exists {
		return candle{}, false
	}
	var values []string
	if json.Unmarshal(raw, &values) != nil || len(values) < 6 {
		return candle{}, false
	}
	ts, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil {
		return candle{}, false
	}
	return candle{Symbol: symbol.Symbol, Interval: "5m", OpenTime: time.UnixMilli(ts).UTC(), Open: number(values[1]), High: number(values[2]), Low: number(values[3]), Close: number(values[4]), Volume: number(values[5])}, true
}

func (s *liquidationService) normalizeOKXLiquidations(raw json.RawMessage) []liquidationEvent {
	var envelope map[string]any
	if json.Unmarshal(raw, &envelope) != nil {
		s.recordDirectParseError("okx", fmt.Errorf("decode liquidation message"))
		return nil
	}
	instID, _ := envelope["instId"].(string)
	details, _ := envelope["details"].([]any)
	if instID == "" {
		return nil
	}
	result := make([]liquidationEvent, 0, len(details))
	for _, rawDetail := range details {
		detail, _ := rawDetail.(map[string]any)
		if detail == nil {
			continue
		}
		price := number(fmt.Sprint(detail["bkPx"]))
		quantity := number(fmt.Sprint(detail["sz"]))
		timestamp, _ := strconv.ParseInt(fmt.Sprint(detail["ts"]), 10, 64)
		if event, ok := s.normalizeOKX(instID, fmt.Sprint(detail["side"]), price, quantity, timestamp); ok {
			result = append(result, event)
		}
	}
	return result
}

func (s *liquidationService) normalizeOKX(instID, orderSide string, price, quantity float64, timestamp int64) (liquidationEvent, bool) {
	s.mu.RLock()
	symbol, exists := s.byOKX[instID]
	s.mu.RUnlock()
	if !exists || price <= 0 || quantity <= 0 || timestamp <= 0 {
		return liquidationEvent{}, false
	}
	side := "long"
	if orderSide == "buy" {
		side = "short"
	}
	occurred := time.UnixMilli(timestamp).UTC()
	contractValue := symbol.OKXContractValue
	if contractValue <= 0 {
		contractValue = 1
	}
	return liquidationEvent{Exchange: "okx", Symbol: symbol.Symbol, Occurred: occurred, Side: side, Price: price, Quantity: quantity, Notional: price * quantity * contractValue, DedupKey: liquidationDedupKey("okx", symbol.Symbol, occurred, price, quantity)}, true
}

func liquidationDedupKey(exchange, symbol string, occurred time.Time, price, quantity float64) string {
	return fmt.Sprintf("%s:%s:%d:%s:%s", exchange, symbol, occurred.UnixMilli(), strconv.FormatFloat(price, 'g', -1, 64), strconv.FormatFloat(quantity, 'g', -1, 64))
}

func (s *liquidationService) handleEvent(ctx context.Context, event liquidationEvent, source string) {
	if s.store == nil {
		return
	}
	inserted, err := s.store.saveEvent(ctx, event)
	if err != nil {
		log.Printf("save liquidation: %v", err)
		return
	}
	s.mu.Lock()
	status := s.statuses[event.Exchange]
	if status.LastEvent.Before(event.Occurred) {
		status.LastEvent = event.Occurred
	}
	if source == "fallback" {
		if status.LastFallbackEvent.Before(event.Occurred) {
			status.LastFallbackEvent = event.Occurred
		}
	} else {
		if status.LastDirectEvent.Before(event.Occurred) {
			status.LastDirectEvent = event.Occurred
		}
		status.Error = ""
	}
	s.statuses[event.Exchange] = status
	if !inserted {
		s.mu.Unlock()
		return
	}
	for subscriber := range s.subscribers {
		if subscriber.symbol == event.Symbol && (len(subscriber.exchanges) == 0 || subscriber.exchanges[event.Exchange]) {
			select {
			case subscriber.updates <- event:
			default:
			}
		}
	}
	s.mu.Unlock()
}

func (s *liquidationService) subscribe(symbol string, exchanges map[string]bool) *liquidationSubscriber {
	subscriber := &liquidationSubscriber{symbol: symbol, exchanges: exchanges, updates: make(chan liquidationEvent, 64)}
	s.mu.Lock()
	s.subscribers[subscriber] = struct{}{}
	s.mu.Unlock()
	return subscriber
}
func (s *liquidationService) unsubscribe(subscriber *liquidationSubscriber) {
	s.mu.Lock()
	delete(s.subscribers, subscriber)
	s.mu.Unlock()
}

func number(value string) float64 {
	result, _ := strconv.ParseFloat(value, 64)
	if math.IsNaN(result) || math.IsInf(result, 0) {
		return 0
	}
	return result
}

func retentionDuration() time.Duration {
	hours, err := strconv.Atoi(strings.TrimSpace(os.Getenv("LIQUIDATION_RETENTION_HOURS")))
	if err != nil || hours <= 0 {
		return liquidationRetention
	}
	return time.Duration(hours) * time.Hour
}

func rangeStart(value string) (time.Time, string, bool) {
	ranges := map[string]time.Duration{"1h": time.Hour, "4h": 4 * time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour}
	if value == "" {
		value = "24h"
	}
	duration, exists := ranges[value]
	return time.Now().UTC().Add(-duration), value, exists
}

func exchangesFromQuery(raw string) (map[string]bool, bool) {
	if raw == "" || raw == "all" {
		return map[string]bool{}, true
	}
	result := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		if part == "binance" || part == "okx" {
			result[part] = true
		} else {
			return nil, false
		}
	}
	return result, len(result) > 0
}

func (s *liquidationService) validSymbol(symbol string) bool {
	for _, item := range s.symbolsSnapshot() {
		if item.Symbol == symbol {
			return true
		}
	}
	return false
}

func (s *liquidationService) serveSymbols(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"symbols": s.symbolsSnapshot(), "status": s.statusSnapshot()})
}
func (s *liquidationService) serveStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.statusSnapshot())
}

func (s *liquidationService) serveChart(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "liquidation database is not configured"})
		return
	}
	symbol := r.URL.Query().Get("symbol")
	if !s.validSymbol(symbol) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown symbol"})
		return
	}
	since, window, ok := rangeStart(r.URL.Query().Get("range"))
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "range must be 1h, 4h, 24h, or 7d"})
		return
	}
	exchanges, ok := exchangesFromQuery(r.URL.Query().Get("exchanges"))
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid exchanges"})
		return
	}
	minimumCandles := int(time.Since(since) / (5 * time.Minute) * 3 / 4)
	if minimumCandles < 1 {
		minimumCandles = 1
	}
	if count, err := s.store.candleCount(r.Context(), symbol, since); err != nil {
		log.Printf("liquidation candle count: %v", err)
	} else if count < minimumCandles {
		if err := s.backfillCandles(r.Context(), symbol, since); err != nil {
			log.Printf("liquidation candle backfill: %v", err)
		}
	}
	candles, events, collectionStart, err := s.store.chart(r.Context(), symbol, since, exchanges)
	if err != nil {
		log.Printf("liquidation chart: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to load liquidation chart"})
		return
	}
	var collectionStartedAt any
	if !collectionStart.IsZero() {
		collectionStartedAt = collectionStart
	}
	writeJSON(w, http.StatusOK, map[string]any{"symbol": symbol, "range": window, "collectionStartedAt": collectionStartedAt, "candles": candles, "events": events, "status": s.statusSnapshot()})
}

func (s *liquidationService) serveLiveWS(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		http.Error(w, "liquidation database is not configured", http.StatusServiceUnavailable)
		return
	}
	symbol := r.URL.Query().Get("symbol")
	exchanges, ok := exchangesFromQuery(r.URL.Query().Get("exchanges"))
	if !s.validSymbol(symbol) || !ok {
		http.Error(w, "invalid subscription", http.StatusBadRequest)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	subscriber := s.subscribe(symbol, exchanges)
	defer s.unsubscribe(subscriber)
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-subscriber.updates:
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			err := wsjson.Write(ctx, conn, map[string]any{"type": "liquidation", "event": event})
			cancel()
			if err != nil {
				return
			}
		}
	}
}
