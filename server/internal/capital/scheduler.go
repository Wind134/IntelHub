package capital

import (
	"context"
	"log"
	"sync"
	"time"
)

// Engine 统筹三地资金流向采集调度（首次冷启动自动回填 + 日常盘中/盘后错峰调度）。
type Engine struct {
	Repo *Repo
	CN   *CNCollector
	US   *USCollector
	HK   *HKCollector

	syncMu sync.Mutex
}

func NewEngine(repo *Repo, tushareToken, secUA string) *Engine {
	return &Engine{
		Repo: repo,
		CN:   NewCNCollector(repo, tushareToken),
		US:   NewUSCollector(repo, secUA),
		HK:   NewHKCollector(repo),
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

	// 定时错峰巡检（按北京时间调度）
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		lastCNEOD := ""
		lastHK := ""
		lastUS := ""
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now().In(shanghaiTZ)
				today := now.Format("2006-01-02")

				// A股盘中 09:35-15:00 每 5 分钟更新实时量比预警
				if _, intraday := ElapsedCNTradingMinutes(now); intraday {
					_ = e.CN.SyncAll(ctx, true)
				}
				// A股盘后 19:30 & 21:30 精确份额+净值双源核验
				if (now.Hour() == 19 && now.Minute() >= 30 || now.Hour() == 21 && now.Minute() >= 30) && lastCNEOD != fmtSlot(today, now.Hour()) {
					lastCNEOD = fmtSlot(today, now.Hour())
					_ = e.TriggerSync(ctx, "cn")
				}
				// 港股盘后 20:30 南向+CCASS 快照
				if now.Hour() == 20 && now.Minute() >= 30 && lastHK != today {
					lastHK = today
					_ = e.TriggerSync(ctx, "hk")
				}
				// 美股早晨 06:30 巡检 SEC 13F 新申报
				if now.Hour() == 6 && now.Minute() >= 30 && lastUS != today {
					lastUS = today
					_ = e.TriggerSync(ctx, "us")
				}
			}
		}
	}()
}

func (e *Engine) TriggerSync(ctx context.Context, market string) error {
	e.syncMu.Lock()
	defer e.syncMu.Unlock()

	sctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	switch market {
	case "cn":
		log.Printf("[capital] syncing A-share National Team ETFs...")
		return e.CN.SyncAll(sctx, false)
	case "us":
		log.Printf("[capital] syncing US SEC 13F institutions...")
		return e.US.SyncAll(sctx)
	case "hk":
		log.Printf("[capital] syncing HKEX Southbound & CCASS...")
		return e.HK.SyncAll(sctx)
	case "all":
		_ = e.CN.SyncAll(sctx, false)
		_ = e.US.SyncAll(sctx)
		_ = e.HK.SyncAll(sctx)
		return nil
	}
	return nil
}

func fmtSlot(day string, hour int) string {
	return day + "-" + string(rune('0'+hour/10)) + string(rune('0'+hour%10))
}
