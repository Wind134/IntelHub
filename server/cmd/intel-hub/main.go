package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"intel-hub/internal/api"
	"intel-hub/internal/capital"
	"intel-hub/internal/collector"
	"intel-hub/internal/config"
	"intel-hub/internal/model"
	"intel-hub/internal/notifier"
	"intel-hub/internal/pipeline"
	"intel-hub/internal/scorer"
	"intel-hub/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "serve":
		runServe(args)
	case "query":
		runQuery(args)
	case "migrate-hermes":
		runMigrateHermes(args)
	case "backup":
		runBackup(args)
	case "budget":
		runBudget(args)
	case "test-push":
		runTestPush(args)
	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`intel-hub — 全天候多领域突发情报中枢 (Golang + SQLite)

Usage:
  intel-hub serve [--config path]               启动采集引擎、HTTP/WSS/SSE 服务与定时热备
  intel-hub query <subcmd> [flags]              Hermes / 运维免依赖只读查库工具
      recent [--hours 24] [--min-score 5]       列出近期高价值突发事件
      event <EVT-CODE>                          查看指定事件全文、核验状态与追问记录
      search <keywords> [--days 90] [--limit 20] CJK+FTS5 全量历史检索
      entity <NAME> [--limit 20]                查看单一实体的历史演进时间轴
      digest-pending                            输出今日待汇总的 5~7 分次级情报
      digest-ack                                标记已汇总进每日内参
      save-digest --title "..." [--date YYYY-MM-DD] (从 stdin 读取 Markdown 存入 daily_digests)
      stats                                     查看系统状态与历史库统计
  intel-hub migrate-hermes [--watcher-dir path] 导入 ~/.hermes/watcher-state/*.json 历史水位线
  intel-hub backup [--out path]                 执行 SQLite VACUUM INTO 一致性热备快照
  intel-hub budget                              查看当月低成本 API 账单与 $3 熔断进度
  intel-hub test-push [--score 9]               生成一条测试突发事件，验证 WS/FCM/飞书通知`)
}

func loadConfigAndStore(cfgPath string) (*config.Config, *store.Store) {
	if cfgPath == "" {
		if envCfg := os.Getenv("INTEL_CONFIG"); envCfg != "" {
			cfgPath = envCfg
		} else {
			defaultPath := "~/.hermes/intel/config.yaml"
			if home, err := os.UserHomeDir(); err == nil {
				candidate := filepath.Join(home, ".hermes", "intel", "config.yaml")
				if _, err := os.Stat(candidate); err == nil {
					cfgPath = defaultPath
				}
			}
		}
	}
	cfg, err := config.Load(cfgPath, "~/.hermes/.env", "~/.hermes/intel/.env")
	if err != nil {
		log.Fatalf("load config failed: %v", err)
	}
	st, err := store.Open(cfg.Server.DBPath)
	if err != nil {
		log.Fatalf("open sqlite store (%s) failed: %v", cfg.Server.DBPath, err)
	}
	return cfg, st
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.yaml")
	_ = fs.Parse(args)

	cfg, st := loadConfigAndStore(*cfgPath)
	defer st.Close()

	hub := api.NewHub()
	llm := scorer.NewClient(cfg.LLM)
	if saved, err := st.GetSetting(context.Background(), "llm_model_config"); err == nil && saved != "" {
		var sel model.ModelSelection
		if err := json.Unmarshal([]byte(saved), &sel); err == nil {
			llm.UpdateSelection(sel)
			log.Printf("[intel-hub] restored isolated LLM model selection from SQLite: provider=%s fast=%s fallback=%s strong=%s",
				sel.Provider, sel.FastModel, sel.FallbackModel, sel.StrongModel)
		}
	}
	enricher := pipeline.NewEnricher(cfg.Search, st)
	feishu := notifier.NewFeishuNotifier(cfg.Notify)
	fcm, err := notifier.NewFCMNotifier(cfg.Notify.FCMServiceAccountPath, cfg.Notify.FCMProjectID)
	if err != nil {
		log.Printf("[fcm] disabled due to init error: %v", err)
	} else if fcm.Enabled() {
		log.Printf("[fcm] HTTP v1 high-priority push enabled (project=%s)", fcm.ProjectID())
	}

	srv := api.NewServer(cfg, st, hub, llm, enricher, feishu, fcm)
	if savedPolicy, err := st.GetSetting(context.Background(), "notification_policy"); err == nil && savedPolicy != "" {
		var p model.NotificationPolicy
		if err := json.Unmarshal([]byte(savedPolicy), &p); err == nil {
			srv.SetNotificationPolicy(p)
			log.Printf("[intel-hub] restored notification policy from SQLite: min_score=%d categories=%v feishu=%v daily_digest=%v",
				p.MinPushScore, p.Categories, p.EnableFeishu, p.EnableDailyDigest)
		}
	}
	engine := pipeline.NewEngine(cfg, st, llm, enricher, srv)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start collectors
	feedPoller := collector.NewFeedPoller(cfg.Feeds, st, engine.ProcessBatch)
	feedPoller.Start(ctx)

	twCollector := collector.NewTwitterAPICollector(cfg.TwitterAPI, cfg.Budget, st, engine.ProcessBatch)
	twCollector.Start(ctx)

	fhCollector := collector.NewFinnhubCollector(cfg.Finnhub, st, engine.ProcessBatch)
	fhCollector.Start(ctx)

	// Daily maintenance loop: 21:00 UTC+8 executive digest + 03:30 UTC+8 backup & fingerprint prune.
	go runMaintenanceLoop(ctx, cfg, st, srv)

	// Capital Flow Web Dashboard (dedicated 127.0.0.1:8788 listener sharing SQLite WAL & encrypted auth)
	capRepo := capital.NewRepo(st)
	if err := capRepo.Migrate(); err != nil {
		log.Fatalf("capital migrate failed: %v", err)
	}
	capEngine := capital.NewEngine(capRepo, cfg.Server.TushareToken, cfg.Server.SECUserAgent)
	capEngine.SetAlertSink(func(ctx context.Context, ev *model.IntelEvent) {
		saved, err := st.InsertEvent(ctx, ev)
		if err != nil || saved == nil {
			return
		}
		srv.OnEventCreated(ctx, saved)
	})
	capEngine.Start(ctx)
	var capSrv *http.Server
	if strings.TrimSpace(cfg.Server.CapitalWebAddr) != "" {
		capWeb := capital.NewWebServer(cfg, st, capEngine)
		capSrv = &http.Server{
			Addr:              cfg.Server.CapitalWebAddr,
			Handler:           capWeb.Handler(),
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			log.Printf("[capital-web] listening on http://%s (strict login gate enabled)", cfg.Server.CapitalWebAddr)
			if err := capSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("[capital-web] server error: %v", err)
			}
		}()
	}

	httpSrv := &http.Server{
		Addr:              cfg.Server.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("[intel-hub] listening on http://%s (db=%s, feeds=%d, twitter=%v, finnhub=%v)",
			cfg.Server.ListenAddr, cfg.Server.DBPath, len(cfg.Feeds), cfg.TwitterAPI.Enabled, cfg.Finnhub.Enabled)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server failed: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("[intel-hub] shutting down gracefully...")
	shutCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutCtx)
	if capSrv != nil {
		_ = capSrv.Shutdown(shutCtx)
	}
}

func runMaintenanceLoop(ctx context.Context, cfg *config.Config, st *store.Store, srv *api.Server) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*3600)
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	lastBackupDay := ""
	lastDigestDay := ""

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().In(shanghai)
			today := now.Format("2006-01-02")

			// Daily 21:00 UTC+8 automated executive digest
			if now.Hour() == 21 && lastDigestDay != today {
				policy := srv.GetNotificationPolicy()
				if policy.EnableDailyDigest {
					lastDigestDay = today
					dctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
					if _, err := srv.GenerateTodayDigest(dctx); err != nil {
						log.Printf("[maint] daily digest skipped/error: %v", err)
					}
					cancel()
				}
			}

			if now.Hour() == 3 && now.Minute() >= 30 && lastBackupDay != today {
				lastBackupDay = today
				if deleted, err := st.PruneOldFingerprints(ctx, 7); err == nil {
					log.Printf("[maint] pruned %d expired dedup fingerprints", deleted)
				}
				if cfg.Server.BackupDir != "" {
					snap := filepath.Join(cfg.Server.BackupDir, fmt.Sprintf("intel_hub_%s.db", now.Format("20060102")))
					if err := st.BackupToFile(ctx, snap); err != nil {
						log.Printf("[maint] backup failed: %v", err)
					} else {
						log.Printf("[maint] hot snapshot created: %s", snap)
					}
				}
			}
		}
	}
}

func runQuery(args []string) {
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}
	sub := args[0]
	subArgs := args[1:]

	fs := flag.NewFlagSet("query-"+sub, flag.ExitOnError)
	cfgPath := fs.String("config", "", "config path")
	hours := fs.Int("hours", 24, "lookback hours")
	days := fs.Int("days", 0, "lookback days (0=all history)")
	minScore := fs.Int("min-score", 5, "minimum score")
	limit := fs.Int("limit", 20, "max results")
	title := fs.String("title", "今日情报内参", "digest title")
	dateStr := fs.String("date", "", "digest date YYYY-MM-DD")
	_ = fs.Parse(subArgs)

	cfg, st := loadConfigAndStore(*cfgPath)
	defer st.Close()
	ctx := context.Background()

	switch sub {
	case "recent":
		fromTS := time.Now().Add(-time.Duration(*hours) * time.Hour).Unix()
		items, err := st.ListEvents(ctx, model.EventFilter{FromTS: fromTS, MinScore: *minScore, Limit: *limit})
		mustJSON(items, err)
	case "event":
		if fs.NArg() < 1 {
			log.Fatal("usage: intel-hub query event <EVT-CODE>")
		}
		ev, err := st.GetEventByCode(ctx, fs.Arg(0))
		if err != nil {
			log.Fatalf("event not found: %v", err)
		}
		threads, _ := st.GetEventThreads(ctx, ev.ID)
		mustJSON(map[string]any{"event": ev, "threads": threads}, nil)
	case "search":
		if fs.NArg() < 1 {
			log.Fatal("usage: intel-hub query search <keywords>")
		}
		var fromTS int64
		if *days > 0 {
			fromTS = time.Now().Add(-time.Duration(*days) * 24 * time.Hour).Unix()
		}
		items, err := st.ListEvents(ctx, model.EventFilter{
			Query:  strings.Join(fs.Args(), " "),
			FromTS: fromTS,
			Sort:   "relevance",
			Limit:  *limit,
		})
		mustJSON(items, err)
	case "entity":
		if fs.NArg() < 1 {
			log.Fatal("usage: intel-hub query entity <NAME>")
		}
		items, err := st.GetEntityTimeline(ctx, fs.Arg(0), 0, 0, *limit)
		mustJSON(items, err)
	case "digest-pending":
		items, err := st.PendingDigestItems(ctx)
		mustJSON(items, err)
	case "digest-ack":
		n, err := st.AckDigestItems(ctx, time.Now().Unix())
		mustJSON(map[string]any{"acked": n}, err)
	case "save-digest":
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			log.Fatal(err)
		}
		pending, _ := st.PendingDigestItems(ctx)
		err = st.SaveDailyDigest(ctx, model.DailyDigest{
			DigestDate: *dateStr,
			Title:      *title,
			ContentMD:  string(raw),
			EventCount: len(pending),
		})
		if err == nil {
			_, _ = st.AckDigestItems(ctx, time.Now().Unix())
		}
		mustJSON(map[string]any{"ok": err == nil, "events_included": len(pending)}, err)
	case "stats":
		ov, err := st.GetSystemOverview(ctx, cfg.Budget.MonthlyCapUSD, 0)
		mustJSON(ov, err)
	default:
		printUsage()
		os.Exit(1)
	}
}

func runMigrateHermes(args []string) {
	fs := flag.NewFlagSet("migrate-hermes", flag.ExitOnError)
	cfgPath := fs.String("config", "", "config path")
	watcherDir := fs.String("watcher-dir", "", "path to ~/.hermes/watcher-state")
	_ = fs.Parse(args)

	_, st := loadConfigAndStore(*cfgPath)
	defer st.Close()

	dir := *watcherDir
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, ".hermes", "watcher-state")
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Fatalf("read watcher-state dir %s: %v", dir, err)
	}

	ctx := context.Background()
	totalIDs := 0
	totalFiles := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		feedName := strings.TrimSuffix(entry.Name(), ".json")
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var state struct {
			SeenIDs []string `json:"seen_ids"`
		}
		if err := json.Unmarshal(raw, &state); err != nil {
			continue
		}
		for _, id := range state.SeenIDs {
			uid := pipeline.SHA256Hex(feedName + ":" + strings.TrimSpace(id))
			urlHash := ""
			if strings.HasPrefix(id, "http") {
				urlHash = pipeline.SHA256Hex(pipeline.CanonicalizeURL(id))
			}
			_ = st.RecordFingerprintOnly(ctx, uid, urlHash, 0, "[migrated] "+feedName)
			totalIDs++
		}
		_ = st.UpsertSourceSuccess(ctx, feedName, "rss", "", "", "")
		totalFiles++
	}
	fmt.Printf("Migrated %d historical watermark IDs from %d feeds in %s\n", totalIDs, totalFiles, dir)
}

func runBackup(args []string) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	cfgPath := fs.String("config", "", "config path")
	out := fs.String("out", "", "destination snapshot path")
	_ = fs.Parse(args)

	cfg, st := loadConfigAndStore(*cfgPath)
	defer st.Close()

	dest := *out
	if dest == "" {
		shanghai := time.FixedZone("Asia/Shanghai", 8*3600)
		dest = filepath.Join(cfg.Server.BackupDir, fmt.Sprintf("intel_hub_%s.db", time.Now().In(shanghai).Format("20060102_150405")))
	}
	if err := st.BackupToFile(context.Background(), dest); err != nil {
		log.Fatalf("backup failed: %v", err)
	}
	fmt.Println(dest)
}

func runBudget(args []string) {
	fs := flag.NewFlagSet("budget", flag.ExitOnError)
	cfgPath := fs.String("config", "", "config path")
	_ = fs.Parse(args)

	cfg, st := loadConfigAndStore(*cfgPath)
	defer st.Close()

	summary, err := st.GetMonthlyBudget(context.Background(), cfg.Budget.MonthlyCapUSD)
	mustJSON(summary, err)
}

func runTestPush(args []string) {
	fs := flag.NewFlagSet("test-push", flag.ExitOnError)
	cfgPath := fs.String("config", "", "config path")
	score := fs.Int("score", 9, "test score 1-10")
	_ = fs.Parse(args)

	cfg, st := loadConfigAndStore(*cfgPath)
	defer st.Close()
	ctx := context.Background()

	ev, err := st.InsertEvent(ctx, &model.IntelEvent{
		SourceName:         "test:manual",
		Category:           "ai",
		RawTitle:           "[Test Alert] IntelHub Native Notification & Historical Sync Check",
		ZhTitle:            "【联调测试】全天候突发情报中枢通知链路畅通",
		Summary:            "这是一条手动触发的端到端测试突发事件，用于验证 Windows 原生 Toast、Android 锁屏通知、飞书卡片及历史库 FTS5 检索。",
		ImpactAnalysis:     "系统运行正常，可直接在详情页测试流式 AI 追问与星标同步。",
		CanonicalURL:       "https://github.com",
		Entities:           []string{"INTELHUB", "NVDA", "OPENAI"},
		Score:              *score,
		PushStatus:         "pushed",
		VerificationStatus: "confirmed",
		VerificationNote:   "人工联调测试事件，无需二次核验。",
		PublishedAt:        time.Now().Unix(),
	})
	if err != nil {
		log.Fatalf("insert test event: %v", err)
	}
	feishu := notifier.NewFeishuNotifier(cfg.Notify)
	if feishu.Enabled() {
		if msgID, err := feishu.SendEventCard(ctx, ev); err != nil {
			log.Printf("[feishu] error: %v", err)
		} else if msgID != "" {
			_ = st.SetFeishuMessageID(ctx, ev.ID, msgID)
		}
	}
	mustJSON(ev, nil)
}

func mustJSON(v any, err error) {
	if err != nil {
		log.Fatal(err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
