package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"intel-hub/internal/config"
	"intel-hub/internal/model"
	"intel-hub/internal/store"
)

type pricePoint struct {
	Price float64
	TS    int64
}

// FinnhubCollector streams real-time US equity quotes over free WebSocket (up to 50 symbols)
// to detect sudden 5m/15m anomalies, and polls Finnhub free REST market news.
type FinnhubCollector struct {
	cfg        config.FinnhubConfig
	store      *store.Store
	handler    Handler
	httpClient *http.Client

	mu           sync.Mutex
	history      map[string][]pricePoint
	lastAlertTS  map[string]int64
	etfSet       map[string]bool
}

func NewFinnhubCollector(cfg config.FinnhubConfig, st *store.Store, handler Handler) *FinnhubCollector {
	etfs := make(map[string]bool, len(cfg.ETFSymbols))
	for _, s := range cfg.ETFSymbols {
		etfs[strings.ToUpper(strings.TrimSpace(s))] = true
	}
	return &FinnhubCollector{
		cfg:         cfg,
		store:       st,
		handler:     handler,
		httpClient:  &http.Client{Timeout: 15 * time.Second},
		history:     make(map[string][]pricePoint),
		lastAlertTS: make(map[string]int64),
		etfSet:      etfs,
	}
}

func (f *FinnhubCollector) Start(ctx context.Context) {
	if !f.cfg.Enabled || strings.TrimSpace(f.cfg.APIKey) == "" {
		return
	}
	if len(f.cfg.Symbols) > 0 {
		go f.runWebSocketLoop(ctx)
	}
	go f.runNewsLoop(ctx)
}

func (f *FinnhubCollector) runWebSocketLoop(ctx context.Context) {
	backoff := 3 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := f.connectAndStream(ctx)
		if ctx.Err() != nil {
			return
		}
		log.Printf("[collector:finnhub_ws] disconnected: %v (reconnecting in %v)", err, backoff)
		_, _ = f.store.UpsertSourceError(ctx, "finnhub_ws", "finnhub_ws", fmt.Sprintf("%v", err))

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 60*time.Second {
			backoff *= 2
		}
	}
}

func (f *FinnhubCollector) connectAndStream(ctx context.Context) error {
	wsURL := "wss://ws.finnhub.io?token=" + f.cfg.APIKey
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "shutdown")

	for _, sym := range f.cfg.Symbols {
		sym = strings.ToUpper(strings.TrimSpace(sym))
		if sym == "" {
			continue
		}
		sub, _ := json.Marshal(map[string]string{"type": "subscribe", "symbol": sym})
		if err := conn.Write(ctx, websocket.MessageText, sub); err != nil {
			return err
		}
	}
	_ = f.store.UpsertSourceSuccess(ctx, "finnhub_ws", "finnhub_ws", "", "", "")
	log.Printf("[collector:finnhub_ws] connected and subscribed to %d symbols", len(f.cfg.Symbols))

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		var msg struct {
			Type string `json:"type"`
			Data []struct {
				Symbol string  `json:"s"`
				Price  float64 `json:"p"`
				TimeMS int64   `json:"t"`
			} `json:"data"`
		}
		if err := json.Unmarshal(data, &msg); err != nil || msg.Type != "trade" {
			continue
		}
		for _, tr := range msg.Data {
			f.observeTrade(ctx, tr.Symbol, tr.Price, tr.TimeMS/1000)
		}
	}
}

func (f *FinnhubCollector) observeTrade(ctx context.Context, symbol string, price float64, ts int64) {
	if price <= 0 {
		return
	}
	if ts <= 0 {
		ts = time.Now().Unix()
	}
	windowSec := int64(f.cfg.WindowMinutes) * 60
	if windowSec <= 0 {
		windowSec = 300
	}

	f.mu.Lock()
	pts := append(f.history[symbol], pricePoint{Price: price, TS: ts})
	// Trim points older than windowSec
	cutoff := ts - windowSec
	idx := 0
	for idx < len(pts) && pts[idx].TS < cutoff {
		idx++
	}
	if idx > 0 {
		pts = append([]pricePoint(nil), pts[idx:]...)
	}
	if len(pts) > 120 {
		pts = pts[len(pts)-120:]
	}
	f.history[symbol] = pts

	if len(pts) < 3 {
		f.mu.Unlock()
		return
	}
	base := pts[0].Price
	pct := (price - base) / base * 100.0
	threshold := f.cfg.StockThresholdPct
	if f.etfSet[symbol] {
		threshold = f.cfg.ETFThresholdPct
	}
	if threshold <= 0 {
		threshold = 3.5
	}

	if math.Abs(pct) < threshold {
		f.mu.Unlock()
		return
	}
	// Avoid firing twice for the same symbol within 15 minutes
	if ts-f.lastAlertTS[symbol] < 900 {
		f.mu.Unlock()
		return
	}
	f.lastAlertTS[symbol] = ts
	sparkline := make([]float64, 0, 12)
	step := len(pts) / 12
	if step < 1 {
		step = 1
	}
	for i := 0; i < len(pts); i += step {
		sparkline = append(sparkline, pts[i].Price)
	}
	f.mu.Unlock()

	dir := "急涨"
	if pct < 0 {
		dir = "急跌"
	}
	snapJSON, _ := json.Marshal(map[string]any{
		"symbol":     symbol,
		"price":      price,
		"base_price": base,
		"pct_change": math.Round(pct*100) / 100,
		"window_min": windowSec / 60,
		"sparkline":  sparkline,
	})

	item := model.RawItem{
		SourceName:       "finnhub:ws",
		SourceType:       "finnhub_ws",
		ExternalID:       fmt.Sprintf("fhws:%s:%d", symbol, ts/300),
		Title:            fmt.Sprintf("%s %d分钟%s %.2f%% (现价 $%.2f)", symbol, windowSec/60, dir, math.Abs(pct), price),
		Summary:          fmt.Sprintf("实时行情监控捕获 %s 在最近 %d 分钟内从 $%.2f 变动至 $%.2f (%.2f%%)。", symbol, windowSec/60, base, price, pct),
		URL:              fmt.Sprintf("https://finance.yahoo.com/quote/%s", symbol),
		PublishedAt:      ts,
		CategoryHint:     "finance",
		BypassRuleFilter: true,
		QuoteSnapshot:    string(snapJSON),
	}
	go f.handler(ctx, []model.RawItem{item})
}

func (f *FinnhubCollector) runNewsLoop(ctx context.Context) {
	interval := time.Duration(f.cfg.NewsIntervalSec) * time.Second
	if interval < 60*time.Second {
		interval = 2 * time.Minute
	}
	f.pollNews(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.pollNews(ctx)
		}
	}
}

func (f *FinnhubCollector) pollNews(ctx context.Context) {
	st, _ := f.store.GetSourceState(ctx, "finnhub_news")
	isFirstRun := st == nil || st.LastSuccessAt == 0

	endpoint := "https://finnhub.io/api/v1/news?category=general&token=" + f.cfg.APIKey
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return
	}
	resp, err := f.httpClient.Do(req)
	if err != nil {
		_, _ = f.store.UpsertSourceError(ctx, "finnhub_news", "json", err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		_, _ = f.store.UpsertSourceError(ctx, "finnhub_news", "json", fmt.Sprintf("http %d", resp.StatusCode))
		return
	}
	var news []struct {
		ID       int64  `json:"id"`
		Headline string `json:"headline"`
		Summary  string `json:"summary"`
		URL      string `json:"url"`
		Datetime int64  `json:"datetime"`
		Related  string `json:"related"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024)).Decode(&news); err != nil {
		_, _ = f.store.UpsertSourceError(ctx, "finnhub_news", "json", err.Error())
		return
	}
	limit := 25
	if len(news) < limit {
		limit = len(news)
	}
	items := make([]model.RawItem, 0, limit)
	for _, n := range news[:limit] {
		items = append(items, model.RawItem{
			SourceName:   "finnhub:news",
			SourceType:   "json",
			ExternalID:   fmt.Sprintf("fhnews:%d", n.ID),
			Title:        n.Headline,
			Summary:      n.Summary,
			URL:          n.URL,
			PublishedAt:  n.Datetime,
			CategoryHint: "finance",
			IsBaseline:   isFirstRun,
		})
	}
	if len(items) > 0 {
		f.handler(ctx, items)
	}
	_ = f.store.UpsertSourceSuccess(ctx, "finnhub_news", "json", "", "", "")
}
