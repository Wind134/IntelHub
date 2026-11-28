package capital

import (
	"context"
	"log"
	"sync"
	"time"
	_ "time/tzdata"
)

var newYorkTZ = loadNewYorkTZ()

func loadNewYorkTZ() *time.Location {
	if loc, err := time.LoadLocation("America/New_York"); err == nil {
		return loc
	}
	return time.FixedZone("America/New_York", -5*3600)
}

func isWeekday(t time.Time) bool {
	wd := t.Weekday()
	return wd != time.Saturday && wd != time.Sunday
}

// ProgressFunc 用于采集器向调度引擎上报细粒度同步进度。
type ProgressFunc func(cur, total int, stage string)

// SyncProgress 描述单个市场当前或最近一次后台同步任务的实时进度。
type SyncProgress struct {
	Market     string `json:"market"`
	Running    bool   `json:"running"`
	Stage      string `json:"stage"`
	Current    int    `json:"current"`
	Total      int    `json:"total"`
	Percent    int    `json:"percent"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
	Error      string `json:"error"`
}

// Engine 统筹三地资金流向采集调度（首次冷启动自动回填 + 日常盘中/盘后错峰调度）。
type Engine struct {
	Repo *Repo
	CN   *CNCollector
	US   *USCollector
	HK   *HKCollector

	syncMu   sync.Mutex
	progMu   sync.RWMutex
	progress map[string]*SyncProgress
}

func NewEngine(repo *Repo, tushareToken, secUA string) *Engine {
	e := &Engine{
		Repo:     repo,
		CN:       NewCNCollector(repo, tushareToken),
		US:       NewUSCollector(repo, secUA),
		HK:       NewHKCollector(repo),
		progress: make(map[string]*SyncProgress),
	}
	e.CN.onProgress = func(cur, total int, stage string) { e.updateProgress("cn_etf", cur, total, stage) }
	e.US.onProgress = func(cur, total int, stage string) { e.updateProgress("us_13f", cur, total, stage) }
	e.HK.onProgress = func(cur, total int, stage string) { e.updateProgress("hk_flow", cur, total, stage) }
	return e
}

func (e *Engine) SetAlertSink(fn AlertSink) {
	if e.CN != nil {
		e.CN.onAlert = fn
	}
}

func (e *Engine) MarkSyncQueued(market string) {
	markets := expandMarkets(market)
	now := time.Now().Unix()
	e.progMu.Lock()
	for _, m := range markets {
		e.progress[m] = &SyncProgress{
			Market:    m,
			Running:   true,
			Stage:     "任务已排队，准备连接官方数据源...",
			Percent:   2,
			StartedAt: now,
		}
	}
	e.progMu.Unlock()
}

func (e *Engine) updateProgress(market string, cur, total int, stage string) {
	e.progMu.Lock()
	p, ok := e.progress[market]
	if !ok {
		p = &SyncProgress{Market: market, StartedAt: time.Now().Unix()}
		e.progress[market] = p
	}
	p.Running = true
	p.Current = cur
	p.Total = total
	p.Stage = stage
	if total > 0 {
		pct := (cur * 100) / total
		if pct < 5 {
			pct = 5
		}
		if pct > 98 {
			pct = 98
		}
		p.Percent = pct
	}
	e.progMu.Unlock()
}

func (e *Engine) finishProgress(market string, err error) {
	e.progMu.Lock()
	p, ok := e.progress[market]
	if !ok {
		p = &SyncProgress{Market: market}
		e.progress[market] = p
	}
	p.Running = false
	p.FinishedAt = time.Now().Unix()
	if err != nil {
		p.Error = err.Error()
		p.Stage = "同步异常"
	} else {
		p.Error = ""
		p.Percent = 100
		if p.Total > 0 {
			p.Current = p.Total
		}
		p.Stage = "同步完成"
	}
	e.progMu.Unlock()
}

func (e *Engine) SnapshotProgress() map[string]SyncProgress {
	e.progMu.RLock()
	defer e.progMu.RUnlock()
	out := make(map[string]SyncProgress, len(e.progress))
	for k, v := range e.progress {
		out[k] = *v
	}
	return out
}

func expandMarkets(market string) []string {
	switch market {
	case "cn":
		return []string{"cn_etf"}
	case "us":
		return []string{"us_13f"}
	case "hk":
		return []string{"hk_flow"}
	default:
		return []string{"cn_etf", "us_13f", "hk_flow"}
	}
}

func (e *Engine) Start(ctx context.Context) {
	// 启动后延迟 8 秒后台执行一次首次冷启动回填（若表为空）
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(8 * time.Second):
		}
		statuses, _ := e.Repo.ListSyncStatuses(ctx)
		has := map[string]bool{}
		for _, s := range statuses {
			if s.Status == "ok" && s.ItemCount > 0 {
				has[s.Market] = true
			}
		}
		if !has["cn_etf"] {
			_ = e.TriggerSync(ctx, "cn")
		}
		if !has["us_13f"] {
			_ = e.TriggerSync(ctx, "us")
		}
		if !has["hk_flow"] {
			_ = e.TriggerSync(ctx, "hk")
		}
	}()

	// 定时错峰巡检：A股/港股严格按北京时间(UTC+8)，美股严格按美东时间(America/New_York，自动适配夏/冬令时)
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		lastCNSlot := ""
		lastHK := ""
		lastUSSlot := ""
		intradayFails := 0
		var intradayCooldownUntil time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now()
				nowBJ := now.In(shanghaiTZ)
				todayBJ := nowBJ.Format("2006-01-02")

				// 1. A股（北京时间，周一至周五）：盘中 09:30-11:30 & 13:00-15:00 每 1 分钟轻量量比观测（仅 1 次批量请求，失败自动退避 5 分钟）
				if _, intraday := ElapsedCNTradingMinutes(now); intraday {
					if now.After(intradayCooldownUntil) {
						if err := e.CN.SyncAll(ctx, true); err != nil {
							intradayFails++
							if intradayFails >= 2 {
								intradayCooldownUntil = now.Add(5 * time.Minute)
							}
						} else {
							intradayFails = 0
						}
					}
				}
				if isWeekday(nowBJ) {
					// A股晨间 08:45 复核深交所/Tushare 隔夜终值 + 盘后 19:30 / 21:30 三源核验
					isCNMorning := nowBJ.Hour() == 8 && nowBJ.Minute() >= 45
					isCNEvening := (nowBJ.Hour() == 19 && nowBJ.Minute() >= 30) || (nowBJ.Hour() == 21 && nowBJ.Minute() >= 30)
					if (isCNMorning || isCNEvening) && lastCNSlot != fmtSlot(todayBJ, nowBJ.Hour()) {
						lastCNSlot = fmtSlot(todayBJ, nowBJ.Hour())
						_ = e.TriggerSync(ctx, "cn")
					}
					// 2. 港股（北京时间，周一至周五）：盘后 20:30 同步南向+CCASS T+2 快照
					if nowBJ.Hour() == 20 && nowBJ.Minute() >= 30 && lastHK != todayBJ {
						lastHK = todayBJ
						_ = e.TriggerSync(ctx, "hk")
					}
				}

				// 3. 美股 SEC 13F（美东时间 America/New_York，周一至周五，自动处理夏令时/冬令时）：
				// SEC EDGAR 17:30 ET 截稿后于 18:15 ET 巡检 + 次日 08:30 ET 复核
				nowET := now.In(newYorkTZ)
				todayET := nowET.Format("2006-01-02")
				if isWeekday(nowET) {
					isUSPostClose := nowET.Hour() == 18 && nowET.Minute() >= 15
					isUSPreOpen := nowET.Hour() == 8 && nowET.Minute() >= 30
					if (isUSPostClose || isUSPreOpen) && lastUSSlot != fmtSlot(todayET, nowET.Hour()) {
						lastUSSlot = fmtSlot(todayET, nowET.Hour())
						_ = e.TriggerSync(ctx, "us")
					}
				}
			}
		}
	}()
}

func (e *Engine) TriggerSync(ctx context.Context, market string) error {
	e.syncMu.Lock()
	defer e.syncMu.Unlock()

	sctx, cancel := context.WithTimeout(ctx, 12*time.Minute)
	defer cancel()

	runOne := func(m string, fn func() error) error {
		e.updateProgress(m, 0, 100, "正在连接官方数据源...")
		err := fn()
		e.finishProgress(m, err)
		return err
	}

	switch market {
	case "cn":
		log.Printf("[capital] syncing A-share National Team ETFs...")
		return runOne("cn_etf", func() error { return e.CN.SyncAll(sctx, false) })
	case "us":
		log.Printf("[capital] syncing US SEC 13F institutions...")
		return runOne("us_13f", func() error { return e.US.SyncAll(sctx) })
	case "hk":
		log.Printf("[capital] syncing HKEX Southbound & CCASS...")
		return runOne("hk_flow", func() error { return e.HK.SyncAll(sctx) })
	case "all":
		_ = runOne("cn_etf", func() error { return e.CN.SyncAll(sctx, false) })
		_ = runOne("us_13f", func() error { return e.US.SyncAll(sctx) })
		_ = runOne("hk_flow", func() error { return e.HK.SyncAll(sctx) })
		return nil
	}
	return nil
}

func fmtSlot(day string, hour int) string {
	return day + "-" + string(rune('0'+hour/10)) + string(rune('0'+hour%10))
}
