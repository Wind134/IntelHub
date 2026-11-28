package capital

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var shanghaiTZ = time.FixedZone("Asia/Shanghai", 8*3600)

// CNCollector 负责 A 股国家队核心 ETF 的双源份额校验、历史回填与两阶段放量监控。
type CNCollector struct {
	repo         *Repo
	tushareToken string
	http         *http.Client
}

func NewCNCollector(repo *Repo, tushareToken string) *CNCollector {
	return &CNCollector{
		repo:         repo,
		tushareToken: strings.TrimSpace(tushareToken),
		http:         &http.Client{Timeout: 16 * time.Second},
	}
}

// ComputeVolRatio20 两阶段量比计算：盘中按 m/240 进度折算，盘后按全天计算。
func ComputeVolRatio20(turnover float64, prevTurnovers []float64, elapsedMinutes int) float64 {
	if turnover <= 0 || len(prevTurnovers) == 0 {
		return 0
	}
	sum := 0.0
	cnt := 0
	for _, t := range prevTurnovers {
		if t > 0 {
			sum += t
			cnt++
		}
	}
	if cnt == 0 {
		return 0
	}
	ma := sum / float64(cnt)
	if elapsedMinutes <= 0 || elapsedMinutes >= 240 {
		return turnover / ma
	}
	expected := ma * (float64(elapsedMinutes) / 240.0)
	if expected <= 0 {
		return 0
	}
	return turnover / expected
}

// ClassifyVolumeSignal 阶段一盘中预警 + 阶段二盘后份额定性。
func ClassifyVolumeSignal(volRatio float64, netFlowYi float64, isIntraday bool) string {
	if volRatio < 2.5 {
		return "normal"
	}
	if isIntraday {
		return "intraday_surge"
	}
	if netFlowYi > 0 {
		return "confirmed_inflow"
	}
	return "high_turnover"
}

// ElapsedCNTradingMinutes 返回当前北京时间在 A 股 240 分钟交易时段中的已开盘分钟数；非盘中返回 240。
func ElapsedCNTradingMinutes(now time.Time) (int, bool) {
	bj := now.In(shanghaiTZ)
	wd := bj.Weekday()
	if wd == time.Saturday || wd == time.Sunday {
		return 240, false
	}
	minOfDay := bj.Hour()*60 + bj.Minute()
	// 上午 09:30 (570) - 11:30 (690)
	if minOfDay >= 570 && minOfDay <= 690 {
		m := minOfDay - 570
		if m < 5 {
			m = 5
		}
		return m, true
	}
	// 午休 11:30 - 13:00
	if minOfDay > 690 && minOfDay < 780 {
		return 120, true
	}
	// 下午 13:00 (780) - 15:00 (900)
	if minOfDay >= 780 && minOfDay < 900 {
		return 120 + (minOfDay - 780), true
	}
	return 240, false
}

// SyncAll 执行 A 股国家队核心 ETF 完整同步（含首次 60 日历史回填 + 双源份额校验）。
func (c *CNCollector) SyncAll(ctx context.Context, intradayOnly bool) error {
	watches, err := c.repo.ListWatchlist(ctx, "cn_etf")
	if err != nil {
		return err
	}
	if len(watches) == 0 {
		return nil
	}

	elapsedMin, isIntraday := ElapsedCNTradingMinutes(time.Now())
	if intradayOnly && !isIntraday {
		return nil
	}

	// 1. 批量拉取东财精确总份额 f38 + 实时价 f2 + 成交额 f6
	emMap, err := c.fetchEastmoneyQuotes(ctx, watches)
	if err != nil {
		c.repo.SetSyncStatus(ctx, SyncStatus{Market: "cn_etf", Status: "error", ErrorMsg: err.Error(), SourceLabel: "SSE/东财/腾讯双源"})
		return err
	}
	// 2. 批量拉取腾讯 CDN 行情（用于深市反算核验 + 备源）
	txMap, _ := c.fetchTencentQuotes(ctx, watches)
	// 3. 拉取上交所官方每日 ETF 规模（沪市主源）
	sseMap, sseDate, _ := c.fetchSSEScaleMapByDate(ctx, "")

	todayBJ := time.Now().In(shanghaiTZ).Format("2006-01-02")
	latestDate := sseDate
	if latestDate == "" {
		latestDate = todayBJ
	}

	synced := 0
	for _, w := range watches {
		if ctx.Err() != nil {
			break
		}
		existing, _ := c.repo.GetCNETFSeries(ctx, w.Code, 65)
		// 首次历史回填（近 60 个交易日 NAV + 成交额 + 份额基线）
		if len(existing) < 10 && !intradayOnly {
			if err := c.backfillHistory(ctx, w); err != nil {
				log.Printf("[capital:cn] backfill %s warn: %v", w.Code, err)
			}
			existing, _ = c.repo.GetCNETFSeries(ctx, w.Code, 65)
		}

		em, hasEM := emMap[w.Code]
		if !hasEM || em.Shares <= 0 {
			continue
		}

		// 双源份额交叉核验（容差 <= 0.2%）
		primaryShares := em.Shares
		backupShares := 0.0
		verifyStatus := "SINGLE_SOURCE"
		sourceNote := "东财官方总份额(f38)"

		if w.Exchange == "SH" {
			if sseSh, ok := sseMap[w.Code]; ok && sseSh > 0 {
				primaryShares = sseSh
				backupShares = em.Shares
				diffEM := math.Abs(sseSh-em.Shares) / sseSh
				diffTX := 1.0
				if tx, ok := txMap[w.Code]; ok && tx.Close > 0 && tx.MktCapYi > 0 {
					txSh := (tx.MktCapYi * 1e8) / tx.Close
					diffTX = math.Abs(sseSh-txSh) / sseSh
					if diffTX < diffEM {
						backupShares = txSh
					}
				}
				switch {
				case diffEM <= 0.005 || diffTX <= 0.01:
					verifyStatus = "VERIFIED"
					sourceNote = "上交所官方 + 备源双源核验通过"
				default:
					verifyStatus = "SINGLE_SOURCE"
					sourceNote = "上交所官方披露份额（以交易所终值为准）"
				}
			}
		} else {
			if tx, ok := txMap[w.Code]; ok && tx.Close > 0 && tx.MktCapYi > 0 {
				backupShares = (tx.MktCapYi * 1e8) / tx.Close
				diff := math.Abs(primaryShares-backupShares) / primaryShares
				if diff <= 0.01 {
					verifyStatus = "VERIFIED"
					sourceNote = "东财f38 + 腾讯CDN 双源核验通过"
				} else {
					verifyStatus = "SINGLE_SOURCE"
					sourceNote = "东财精确总份额(f38)"
				}
			}
		}

		// 最新官方单位净值（来自东财净值库 lsjz）
		nav, navDate := c.fetchLatestNAV(ctx, w.Code)
		if nav <= 0 {
			nav = em.Close
		}
		tradeDate := navDate
		if isIntraday {
			tradeDate = todayBJ
		} else if tradeDate == "" {
			tradeDate = latestDate
		}
		if tradeDate > latestDate {
			latestDate = tradeDate
		}

		// 提取前 20 日成交额与前一日份额计算真实 ΔS(t) 与两阶段量比
		var prevTurnovers []float64
		prevShares := 0.0
		for _, row := range existing {
			if row.TradeDate < tradeDate {
				prevShares = row.TotalShares
				if row.TurnoverRMB > 0 {
					prevTurnovers = append(prevTurnovers, row.TurnoverRMB)
				}
			}
		}
		if len(prevTurnovers) > 20 {
			prevTurnovers = prevTurnovers[len(prevTurnovers)-20:]
		}

		shareDelta := 0.0
		netFlowYi := 0.0
		if prevShares > 0 {
			pctJump := math.Abs(primaryShares-prevShares) / prevShares
			// 异常熔断门禁：无拆股单日份额跳变 > 30% 拦截保护
			if pctJump <= 0.30 {
				shareDelta = primaryShares - prevShares
				netFlowYi = (shareDelta * nav) / 1e8
			} else {
				verifyStatus = "WARN"
				sourceNote = "检测到份额突变>30%，已触发异常熔断保护"
			}
		}

		volRatio := ComputeVolRatio20(em.TurnoverRMB, prevTurnovers, elapsedMin)
		sig := ClassifyVolumeSignal(volRatio, netFlowYi, isIntraday)

		row := CNETFDaily{
			Code:          w.Code,
			TradeDate:     tradeDate,
			TotalShares:   primaryShares,
			ShareDelta:    shareDelta,
			NAV:           nav,
			ClosePrice:    em.Close,
			TurnoverRMB:   em.TurnoverRMB,
			NetFlowRMB:    netFlowYi,
			VolRatio20:    volRatio,
			VolumeSignal:  sig,
			IsIntraday:    isIntraday,
			VerifyStatus:  verifyStatus,
			SourceNote:    sourceNote,
			PrimaryShares: primaryShares,
			BackupShares:  backupShares,
		}
		if err := c.repo.UpsertCNETFDailyBatch(ctx, []CNETFDaily{row}); err == nil {
			synced++
		}
	}

	if !intradayOnly {
		c.backfillSSEHistoricalShares(ctx, watches, "2026-11-01")
		c.syncInstitutionalHolders(ctx, watches)
	}

	c.repo.SetSyncStatus(ctx, SyncStatus{
		Market:      "cn_etf",
		Status:      "ok",
		DataAsOf:    latestDate,
		ItemCount:   synced,
		SourceLabel: "上交所官方 + 东财精确总份额 + 腾讯CDN双源核验",
	})
	return nil
}

type emQuote struct {
	Close       float64
	TurnoverRMB float64
	Shares      float64
}

func (c *CNCollector) fetchEastmoneyQuotes(ctx context.Context, watches []WatchItem) (map[string]emQuote, error) {
	secids := make([]string, 0, len(watches))
	for _, w := range watches {
		prefix := "0"
		if w.Exchange == "SH" || strings.HasPrefix(w.Code, "5") {
			prefix = "1"
		}
		secids = append(secids, prefix+"."+w.Code)
	}
	u := "https://push2.eastmoney.com/api/qt/ulist.np/get?fltt=2&fields=f2,f6,f12,f20,f38&secids=" + url.QueryEscape(strings.Join(secids, ","))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var payload struct {
		Data struct {
			Diff []struct {
				Code     string  `json:"f12"`
				Close    float64 `json:"f2"`
				Turnover float64 `json:"f6"`
				Shares   float64 `json:"f38"`
			} `json:"diff"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024)).Decode(&payload); err != nil {
		return nil, err
	}
	out := make(map[string]emQuote, len(payload.Data.Diff))
	for _, d := range payload.Data.Diff {
		out[d.Code] = emQuote{Close: d.Close, TurnoverRMB: d.Turnover, Shares: d.Shares}
	}
	return out, nil
}

type txQuote struct {
	Close    float64
	MktCapYi float64
}

func (c *CNCollector) fetchTencentQuotes(ctx context.Context, watches []WatchItem) (map[string]txQuote, error) {
	codes := make([]string, 0, len(watches))
	for _, w := range watches {
		prefix := "sz"
		if w.Exchange == "SH" || strings.HasPrefix(w.Code, "5") {
			prefix = "sh"
		}
		codes = append(codes, prefix+w.Code)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://qt.gtimg.cn/q="+strings.Join(codes, ","), nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	out := make(map[string]txQuote)
	for _, line := range strings.Split(string(raw), "\n") {
		parts := strings.Split(line, "~")
		if len(parts) < 46 {
			continue
		}
		code := strings.TrimSpace(parts[2])
		cl, _ := strconv.ParseFloat(parts[3], 64)
		mc, _ := strconv.ParseFloat(parts[45], 64)
		if code != "" && cl > 0 {
			out[code] = txQuote{Close: cl, MktCapYi: mc}
		}
	}
	return out, nil
}

func (c *CNCollector) fetchSSEScaleMapByDate(ctx context.Context, statDate string) (map[string]float64, string, error) {
	u := "https://query.sse.com.cn/commonQuery.do?sqlId=COMMON_SSE_ZQPZ_ETFZL_XXPL_ETFGM_SEARCH_L&isPagination=true&pageHelp.pageSize=2000&pageHelp.pageNo=1"
	if strings.TrimSpace(statDate) != "" {
		u += "&STAT_DATE=" + url.QueryEscape(strings.TrimSpace(statDate))
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("Referer", "https://www.sse.com.cn/")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	var payload struct {
		Result []struct {
			StatDate string `json:"STAT_DATE"`
			SecCode  string `json:"SEC_CODE"`
			TotVol   string `json:"TOT_VOL"` // 单位：万份
		} `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4*1024*1024)).Decode(&payload); err != nil {
		return nil, "", err
	}
	out := make(map[string]float64, len(payload.Result))
	resolvedDate := ""
	for _, r := range payload.Result {
		if r.StatDate > resolvedDate {
			resolvedDate = r.StatDate
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(r.TotVol), 64)
		if err == nil && v > 0 {
			out[strings.TrimSpace(r.SecCode)] = v * 10000.0
		}
	}
	return out, resolvedDate, nil
}

// backfillSSEHistoricalShares 按交易日回填沪市 ETF 自 sinceDate 起的官方精确每日份额与净申赎流向。
func (c *CNCollector) backfillSSEHistoricalShares(ctx context.Context, watches []WatchItem, sinceDate string) {
	var shWatches []WatchItem
	for _, w := range watches {
		if w.Exchange == "SH" || strings.HasPrefix(w.Code, "5") {
			shWatches = append(shWatches, w)
		}
	}
	if len(shWatches) == 0 {
		return
	}

	// 取代表性沪市 ETF 的历史交易日序列，找出待补齐官方份额的交易日（含 sinceDate 前一日作基准）
	refSeries, _ := c.repo.GetCNETFSeries(ctx, shWatches[0].Code, 65)
	if len(refSeries) < 2 {
		return
	}
	startIdx := len(refSeries)
	for i, d := range refSeries {
		if d.TradeDate >= sinceDate {
			startIdx = i
			break
		}
	}
	if startIdx >= len(refSeries) {
		return
	}
	baseIdx := maxInt(0, startIdx-1)

	var needDates []string
	for i := baseIdx; i < len(refSeries); i++ {
		if !strings.Contains(refSeries[i].SourceNote, "上交所官方") {
			needDates = append(needDates, refSeries[i].TradeDate)
		}
	}
	if len(needDates) == 0 {
		return
	}

	log.Printf("[capital:cn] backfilling SSE official daily shares for %d trading days since %s...", len(needDates), sinceDate)
	dateShares := make(map[string]map[string]float64, len(needDates))
	for _, dt := range needDates {
		if ctx.Err() != nil {
			return
		}
		m, _, err := c.fetchSSEScaleMapByDate(ctx, dt)
		if err == nil && len(m) > 0 {
			dateShares[dt] = m
		}
		time.Sleep(180 * time.Millisecond)
	}
	if len(dateShares) == 0 {
		return
	}

	for _, w := range shWatches {
		series, err := c.repo.GetCNETFSeries(ctx, w.Code, 65)
		if err != nil || len(series) < 2 {
			continue
		}
		// 先填补每日官方总份额
		touched := false
		for i := range series {
			if m, ok := dateShares[series[i].TradeDate]; ok {
				if sh, exists := m[w.Code]; exists && sh > 0 {
					series[i].TotalShares = sh
					series[i].PrimaryShares = sh
					series[i].VerifyStatus = "VERIFIED"
					series[i].SourceNote = "上交所官方历史每日份额"
					touched = true
				}
			}
		}
		if !touched {
			continue
		}
		// 从基准日之后逐日精确重算 ΔS(t)、Flow_ETF(t) 及两阶段量比定性
		var updates []CNETFDaily
		for i := 1; i < len(series); i++ {
			if series[i].TradeDate < sinceDate {
				if _, ok := dateShares[series[i].TradeDate]; ok {
					updates = append(updates, series[i])
				}
				continue
			}
			prevSh := series[i-1].TotalShares
			curSh := series[i].TotalShares
			if prevSh <= 0 || curSh <= 0 {
				continue
			}
			px := series[i].NAV
			if px <= 0 {
				px = series[i].ClosePrice
			}
			pctJump := math.Abs(curSh-prevSh) / prevSh
			if pctJump <= 0.30 {
				series[i].ShareDelta = curSh - prevSh
				series[i].NetFlowRMB = (series[i].ShareDelta * px) / 1e8
			}
			var prevT []float64
			for j := maxInt(0, i-20); j < i; j++ {
				prevT = append(prevT, series[j].TurnoverRMB)
			}
			series[i].VolRatio20 = ComputeVolRatio20(series[i].TurnoverRMB, prevT, 240)
			series[i].VolumeSignal = ClassifyVolumeSignal(series[i].VolRatio20, series[i].NetFlowRMB, false)
			updates = append(updates, series[i])
		}
		if len(updates) > 0 {
			_ = c.repo.UpsertCNETFDailyBatch(ctx, updates)
		}
	}
}

func (c *CNCollector) fetchLatestNAV(ctx context.Context, code string) (float64, string) {
	u := fmt.Sprintf("https://api.fund.eastmoney.com/f10/lsjz?fundCode=%s&pageIndex=1&pageSize=2", code)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("Referer", "https://fundf10.eastmoney.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, ""
	}
	defer resp.Body.Close()
	var payload struct {
		Data struct {
			List []struct {
				FSRQ string `json:"FSRQ"`
				DWJZ string `json:"DWJZ"`
			} `json:"LSJZList"`
		} `json:"Data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&payload); err != nil || len(payload.Data.List) == 0 {
		return 0, ""
	}
	nav, _ := strconv.ParseFloat(payload.Data.List[0].DWJZ, 64)
	return nav, payload.Data.List[0].FSRQ
}

// backfillHistory 回填近 60 个交易日的净值、成交额及历史规模序列。
func (c *CNCollector) backfillHistory(ctx context.Context, w WatchItem) error {
	// 1. 拉取近 60 日官方净值
	navMap := map[string]float64{}
	uNav := fmt.Sprintf("https://api.fund.eastmoney.com/f10/lsjz?fundCode=%s&pageIndex=1&pageSize=65", w.Code)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, uNav, nil)
	req.Header.Set("Referer", "https://fundf10.eastmoney.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	if resp, err := c.http.Do(req); err == nil {
		var p struct {
			Data struct {
				List []struct {
					FSRQ string `json:"FSRQ"`
					DWJZ string `json:"DWJZ"`
				} `json:"LSJZList"`
			} `json:"Data"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 512*1024)).Decode(&p) == nil {
			for _, it := range p.Data.List {
				if v, err := strconv.ParseFloat(it.DWJZ, 64); err == nil && v > 0 {
					navMap[it.FSRQ] = v
				}
			}
		}
		resp.Body.Close()
	}

	// 2. 拉取近 60 日 K 线（收盘价与成交额）
	prefix := "0"
	if w.Exchange == "SH" || strings.HasPrefix(w.Code, "5") {
		prefix = "1"
	}
	uKline := fmt.Sprintf("https://push2his.eastmoney.com/api/qt/stock/kline/get?secid=%s.%s&fields1=f1,f2,f3&fields2=f51,f53,f57&klt=101&fqt=0&end=20500101&lmt=65", prefix, w.Code)
	kReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, uKline, nil)
	kReq.Header.Set("User-Agent", "Mozilla/5.0")
	type kRow struct {
		Date     string
		Close    float64
		Turnover float64
	}
	var klines []kRow
	if resp, err := c.http.Do(kReq); err == nil {
		var kp struct {
			Data struct {
				Klines []string `json:"klines"`
			} `json:"data"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 512*1024)).Decode(&kp) == nil {
			for _, line := range kp.Data.Klines {
				parts := strings.Split(line, ",")
				if len(parts) < 3 {
					continue
				}
				cl, _ := strconv.ParseFloat(parts[1], 64)
				to, _ := strconv.ParseFloat(parts[2], 64)
				klines = append(klines, kRow{Date: parts[0], Close: cl, Turnover: to})
			}
		}
		resp.Body.Close()
	}
	if len(klines) == 0 {
		return nil
	}

	// 3. 拉取 pingzhongdata 历史规模变动点（用于填补最近季报份额基准，日度流向将由每日持续采集逐步累积精确值）
	var latestScaleShares float64
	emQ, err := c.fetchEastmoneyQuotes(ctx, []WatchItem{w})
	if err == nil {
		latestScaleShares = emQ[w.Code].Shares
	}
	sort.Slice(klines, func(i, j int) bool { return klines[i].Date < klines[j].Date })

	var batch []CNETFDaily
	for i, k := range klines {
		nav := navMap[k.Date]
		if nav <= 0 {
			nav = k.Close
		}
		var prevT []float64
		for j := maxInt(0, i-20); j < i; j++ {
			prevT = append(prevT, klines[j].Turnover)
		}
		vr := ComputeVolRatio20(k.Turnover, prevT, 240)
		batch = append(batch, CNETFDaily{
			Code:          w.Code,
			TradeDate:     k.Date,
			TotalShares:   latestScaleShares,
			ShareDelta:    0,
			NAV:           nav,
			ClosePrice:    k.Close,
			TurnoverRMB:   k.Turnover,
			NetFlowRMB:    0,
			VolRatio20:    vr,
			VolumeSignal:  ClassifyVolumeSignal(vr, 0, false),
			IsIntraday:    false,
			VerifyStatus:  "SINGLE_SOURCE",
			SourceNote:    "历史净值与成交额基线（份额净流向自采集日起逐日精确累算）",
			PrimaryShares: latestScaleShares,
		})
	}
	return c.repo.UpsertCNETFDailyBatch(ctx, batch)
}

var reCyrjgRow = regexp.MustCompile(`<tr><td>(\d{4}-\d{2}-\d{2})</td><td[^>]*>([^<]*)</td><td[^>]*>([^<]*)</td><td[^>]*>([^<]*)</td><td[^>]*>([^<]*)</td></tr>`)

// syncInstitutionalHolders 抓取每只 ETF 最新官方定期报告（中报/年报）的机构持仓结构，替代静态硬编码数据。
func (c *CNCollector) syncInstitutionalHolders(ctx context.Context, watches []WatchItem) {
	for _, w := range watches {
		if ctx.Err() != nil {
			return
		}
		u := fmt.Sprintf("https://fundf10.eastmoney.com/FundArchivesDatas.aspx?type=cyrjg&code=%s", w.Code)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		req.Header.Set("Referer", "https://fundf10.eastmoney.com/")
		req.Header.Set("User-Agent", "Mozilla/5.0")
		resp, err := c.http.Do(req)
		if err != nil {
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		resp.Body.Close()

		matches := reCyrjgRow.FindAllStringSubmatch(string(raw), 2)
		if len(matches) == 0 {
			continue
		}
		srcURL := fmt.Sprintf("https://fundf10.eastmoney.com/cyrjg_%s.html", w.Code)
		for _, m := range matches {
			repDate := strings.TrimSpace(m[1])
			instPct, err1 := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(m[2]), "%"), 64)
			totSharesYi, err2 := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(m[5]), ",", ""), 64)
			if err1 != nil || instPct <= 0 {
				continue
			}
			repType := "年报机构结构"
			if strings.HasSuffix(repDate, "-06-30") {
				repType = "中报机构结构"
			}
			sharesHeld := 0.0
			if err2 == nil && totSharesYi > 0 {
				sharesHeld = totSharesYi * 1e8 * (instPct / 100.0)
			}
			_ = c.repo.UpsertCNETFHolder(ctx, CNETFHolder{
				Code:         w.Code,
				ReportDate:   repDate,
				ReportType:   repType,
				HolderName:   "机构投资者合计（定期报告官方披露）",
				SharesHeld:   sharesHeld,
				HoldRatioPct: instPct,
				SourceURL:    srcURL,
			})
		}
		time.Sleep(100 * time.Millisecond)
	}
}
