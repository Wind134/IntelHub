package capital

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
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

// HKCollector 对接港交所披露易官方南向港股通 + CCASS 四大席位阵营（含富途 B01955 / 盈透 B01590）。
type HKCollector struct {
	repo *Repo
	http *http.Client
}

func NewHKCollector(repo *Repo) *HKCollector {
	return &HKCollector{
		repo: repo,
		http: &http.Client{Timeout: 25 * time.Second},
	}
}

// ClassifyHKBrokerCohort 将港交所 CCASS 参与者无重叠划归为四大阵营。
func ClassifyHKBrokerCohort(participantID, participantName string) string {
	id := strings.ToUpper(strings.TrimSpace(participantID))
	name := strings.ToUpper(strings.TrimSpace(participantName))

	// 1. 南向港股通专户
	if id == "C00019" || id == "A00003" || id == "A00004" ||
		strings.Contains(name, "中国证券登记结算") || strings.Contains(name, "中國證券登記結算") ||
		strings.Contains(name, "CHINA SECURITIES DEPOSITORY") {
		return "southbound"
	}

	// 2. 互联网与高活跃券商（富途 B01955 / 盈透 B01590 / 辉立 / 耀才 / 老虎 / 华盛）
	internetIDs := map[string]bool{
		"B01955": true, // 富途证券国际(香港)
		"B01590": true, // 盈透证券香港 (Interactive Brokers)
		"B01815": true, // 耀才证券
		"B01345": true, // 辉立证券
		"B02124": true, // 老虎证券
		"B02077": true, // 华盛资本
		"B01980": true, // 长桥/微牛等活跃互联网券商
	}
	if internetIDs[id] {
		return "internet_retail"
	}
	for _, kw := range []string{"富途", "FUTU", "INTERACTIVE BROKERS", "盈透", "耀才", "BRIGHT SMART", "輝立", "辉立", "PHILLIP", "老虎", "TIGER BROKERS", "華盛", "华盛", "VALUABLE CAPITAL", "WEBULL", "微牛", "長橋", "长桥", "LONGBRIDGE"} {
		if strings.Contains(name, kw) {
			return "internet_retail"
		}
	}

	// 3. 国际外资顶级投行/长线托管
	foreignIDs := map[string]bool{
		"C00001": true, // HSBC 汇丰
		"C00010": true, // Citibank 花旗
		"C00100": true, // JPMorgan 摩根大通
		"C00039": true, // Standard Chartered 渣打
		"B01451": true, // Goldman Sachs 高盛
		"B01274": true, // Morgan Stanley 摩根士丹利
		"B01161": true, // UBS Securities HK 瑞银证券
		"B01555": true, // UBS 瑞银
		"C00018": true, // BNP Paribas 法巴
		"C00033": true, // Deutsche Bank 德银
		"B01224": true, // Merrill Lynch 美银
	}
	if foreignIDs[id] {
		return "foreign_inst"
	}
	for _, kw := range []string{"HSBC", "HONGKONG AND SHANGHAI", "匯豐", "汇丰", "CITIBANK", "花旗", "JPMORGAN", "摩根大通", "STANDARD CHARTERED", "渣打", "GOLDMAN SACHS", "高盛", "MORGAN STANLEY", "摩根士丹利", "UBS", "瑞銀", "瑞银", "MERRILL LYNCH", "美銀", "DEUTSCHE BANK", "德意志", "BNP PARIBAS", "法國巴黎", "BARCLAYS", "巴克萊", "CREDIT SUISSE", "NOMURA", "MACQUARIE"} {
		if strings.Contains(name, kw) {
			return "foreign_inst"
		}
	}

	// 4. 中资在港机构席位（不含港股通）
	for _, kw := range []string{"中銀", "中银", "BANK OF CHINA", "中金", "CHINA INTERNATIONAL CAPITAL", "中信", "CLSA", "CITIC", "招銀", "招银", "CMB", "華泰", "华泰", "HUATAI", "國泰君安", "国泰君安", "GUOTAI", "海通", "HAITONG", "工銀", "工银", "ICBC", "建銀", "建银", "CCB", "農銀", "农银", "交銀", "交银", "招商", "廣發", "广发", "光大", "申萬", "申万", "國信", "国信"} {
		if strings.Contains(name, kw) {
			return "cn_inst"
		}
	}

	return "other"
}

// SettlementToTradeDate 将港股 CCASS T+2 交收日回推 2 个工作日得到对应交易日。
func SettlementToTradeDate(settleDate string) string {
	t, err := time.Parse("2006-01-02", settleDate)
	if err != nil {
		return settleDate
	}
	steps := 0
	for steps < 2 {
		t = t.AddDate(0, 0, -1)
		if t.Weekday() != time.Saturday && t.Weekday() != time.Sunday {
			steps++
		}
	}
	return t.Format("2006-01-02")
}

// DetectCCASSAnomalies 执行公式 3.3 的跨席位转仓与 ADR 转换识别。
func DetectCCASSAnomalies(brokers []HKBrokerPosition, totalCCASSDelta, dailyMarketVol float64) (flag string, note string, transferIDs map[string]bool) {
	transferIDs = make(map[string]bool)
	if dailyMarketVol <= 0 {
		return "", "", transferIDs
	}

	// 场景 B：ADR 转换 / 实物存取仓（CCASS 总量变动 > 80% 当日市场总成交量）
	if math.Abs(totalCCASSDelta) > 0.80*dailyMarketVol {
		return "ADR_CONVERSION",
			fmt.Sprintf("当日 CCASS 总存管变动 %.1f 万股，超过对应交易日全市场成交量 80%%，疑似 ADR 转换或实物存取仓", totalCCASSDelta/1e4),
			transferIDs
	}

	// 场景 A：跨席位对倒转托管（一增一减差额 <= 5% 且单边 > 50% 当日成交量）
	var negs, poss []HKBrokerPosition
	for _, b := range brokers {
		if b.ShareDelta < -0.50*dailyMarketVol {
			negs = append(negs, b)
		} else if b.ShareDelta > 0.50*dailyMarketVol {
			poss = append(poss, b)
		}
	}
	for _, n := range negs {
		for _, p := range poss {
			mx := math.Max(math.Abs(n.ShareDelta), math.Abs(p.ShareDelta))
			if mx > 0 && math.Abs(n.ShareDelta+p.ShareDelta)/mx <= 0.05 {
				transferIDs[n.ParticipantID] = true
				transferIDs[p.ParticipantID] = true
				return "CUSTODIAN_TRANSFER",
					fmt.Sprintf("检测到 %s 与 %s 之间发生约 %.1f 万股跨席位场外转仓，已从净流向中自动剔除", n.ParticipantName, p.ParticipantName, mx/1e4),
					transferIDs
			}
		}
	}
	return "", "", transferIDs
}

type hkQuote struct {
	Close    float64
	Turnover float64 // HKD
	Volume   float64 // shares
	VWAP     float64 // HKD/share
}

func (c *HKCollector) fetchHKQuotes(ctx context.Context, codes []string) map[string]hkQuote {
	out := make(map[string]hkQuote, len(codes))
	// 分批每次最多 80 只拉取东财港股行情（精确总成交额 f6 与总成交股数 f5 -> 真实当日 VWAP）
	for i := 0; i < len(codes); i += 80 {
		end := i + 80
		if end > len(codes) {
			end = len(codes)
		}
		secids := make([]string, 0, end-i)
		for _, cd := range codes[i:end] {
			secids = append(secids, "116."+padHKCode(cd))
		}
		u := "https://push2.eastmoney.com/api/qt/ulist.np/get?fltt=2&fields=f2,f5,f6,f12&secids=" + url.QueryEscape(strings.Join(secids, ","))
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0")
		resp, err := c.http.Do(req)
		if err != nil {
			continue
		}
		var payload struct {
			Data struct {
				Diff []struct {
					Code     string  `json:"f12"`
					Close    float64 `json:"f2"`
					Vol      float64 `json:"f5"`
					Turnover float64 `json:"f6"`
				} `json:"diff"`
			} `json:"data"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024)).Decode(&payload) == nil {
			for _, d := range payload.Data.Diff {
				vwap := d.Close
				if d.Vol > 0 && d.Turnover > 0 {
					v := d.Turnover / d.Vol
					if v > 0 && d.Close > 0 && math.Abs(v-d.Close)/d.Close < 0.25 {
						vwap = v
					}
				}
				out[padHKCode(d.Code)] = hkQuote{
					Close:    d.Close,
					Turnover: d.Turnover,
					Volume:   d.Vol,
					VWAP:     vwap,
				}
			}
		}
		resp.Body.Close()
	}
	return out
}

// SyncAll 同步港交所南向港股通全量持股 + 核心龙头 CCASS 四大阵营席位快照。
func (c *HKCollector) SyncAll(ctx context.Context) error {
	const sbURL = "https://www3.hkexnews.hk/sdw/search/mutualmarket_c.aspx?t=hk"

	// 1. 拉取最新一期南向全量表
	body, err := c.getHTML(ctx, sbURL)
	if err != nil {
		c.repo.SetSyncStatus(ctx, SyncStatus{Market: "hk_flow", Status: "error", ErrorMsg: err.Error(), SourceLabel: "港交所披露易官方"})
		return err
	}
	settleDate, items := parseMutualMarketHTML(body)
	if settleDate == "" || len(items) == 0 {
		err := fmt.Errorf("failed to parse HKEX mutualmarket page")
		c.repo.SetSyncStatus(ctx, SyncStatus{Market: "hk_flow", Status: "error", ErrorMsg: err.Error(), SourceLabel: "港交所披露易官方"})
		return err
	}

	// 若历史少于 2 天，按 ViewState 回拉前 1 个交收日以立即计算精确单日 ΔN
	prevShares, _ := c.repo.GetPrevHKSouthboundShares(ctx, items[0].Code, settleDate)
	if prevShares == 0 {
		if prevSettle, prevItems, perr := c.fetchMutualMarketForDate(ctx, sbURL, body, prevBusinessDay(settleDate)); perr == nil && len(prevItems) > 0 && prevSettle < settleDate {
			c.persistSouthboundSnapshot(ctx, prevSettle, prevItems, sbURL)
		}
	}
	count := c.persistSouthboundSnapshot(ctx, settleDate, items, sbURL)

	// 2. 同步核心关注港股的 CCASS 四大阵营（含富途 B01955 / 盈透 B01590），复用表单状态并跳过已同步标的
	watches, _ := c.repo.ListWatchlist(ctx, "hk_stock")
	quotes := c.fetchHKQuotes(ctx, watchCodes(watches))
	const ccURL = "https://www3.hkexnews.hk/sdw/search/searchsdw_c.aspx"
	initHTML, initErr := c.getHTML(ctx, ccURL)
	if initErr != nil {
		log.Printf("[capital:hk] ccass init warn: %v", initErr)
	} else {
		baseFields := extractAspNetHiddenFields(initHTML)
		for i := range watches {
			if ctx.Err() != nil {
				break
			}
			code := padHKCode(watches[i].Code)
			if _, snaps, _, err := c.repo.GetHKStockDetail(ctx, code); err == nil && len(snaps) > 0 && snaps[0].SettlementDate == settleDate {
				continue
			}
			if err := c.syncOneCCASS(ctx, watches[i], settleDate, quotes[code], baseFields); err != nil {
				log.Printf("[capital:hk] ccass %s warn: %v", watches[i].Code, err)
			}
			time.Sleep(250 * time.Millisecond)
		}
	}

	c.repo.SetSyncStatus(context.Background(), SyncStatus{
		Market:      "hk_flow",
		Status:      "ok",
		DataAsOf:    settleDate,
		ItemCount:   count,
		SourceLabel: "港交所披露易官方南向 + CCASS(含富途/外资大行)",
	})
	return nil
}

func (c *HKCollector) persistSouthboundSnapshot(ctx context.Context, settleDate string, items []rawSBItem, srcURL string) int {
	codes := make([]string, 0, len(items))
	for _, it := range items {
		codes = append(codes, it.Code)
	}
	quotes := c.fetchHKQuotes(ctx, codes)
	tradeDate := SettlementToTradeDate(settleDate)

	batch := make([]HKSouthboundDaily, 0, len(items))
	for _, it := range items {
		prevSh, _ := c.repo.GetPrevHKSouthboundShares(ctx, it.Code, settleDate)
		delta := 0.0
		if prevSh > 0 {
			delta = it.Shares - prevSh
		}
		q := quotes[it.Code]
		px := q.VWAP
		if px <= 0 {
			px = q.Close
		}
		holdValYi := (it.Shares * q.Close) / 1e8
		netFlowYi := (delta * px) / 1e8
		batch = append(batch, HKSouthboundDaily{
			Code:           it.Code,
			Name:           it.Name,
			SettlementDate: settleDate,
			TradeDate:      tradeDate,
			SharesHeld:     it.Shares,
			ShareDelta:     delta,
			HoldRatioPct:   it.RatioPct,
			ClosePriceHKD:  q.Close,
			VWAPHKD:        px,
			TurnoverHKD:    q.Turnover,
			VolumeShares:   q.Volume,
			HoldValueHKDYi: holdValYi,
			NetFlowHKDYi:   netFlowYi,
			SourceURL:      srcURL,
		})
	}
	_ = c.repo.UpsertHKSouthboundBatch(ctx, batch)
	return len(batch)
}

func (c *HKCollector) syncOneCCASS(ctx context.Context, w WatchItem, settleDate string, q hkQuote, baseFields url.Values) error {
	const ccURL = "https://www3.hkexnews.hk/sdw/search/searchsdw_c.aspx"
	form := url.Values{}
	for k, vals := range baseFields {
		form[k] = append([]string(nil), vals...)
	}
	dateSlash := strings.ReplaceAll(settleDate, "-", "/")
	form.Set("txtShareholdingDate", dateSlash)
	form.Set("txtStockCode", padHKCode(w.Code))
	form.Set("btnSearch", "搜尋")
	form.Set("__EVENTTARGET", "btnSearch")
	form.Set("sortBy", "shareholding")
	form.Set("sortDirection", "desc")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ccURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", ccURL)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	brokers := parseCCASSHTML(string(raw), padHKCode(w.Code), settleDate)
	if len(brokers) == 0 {
		return fmt.Errorf("empty ccass table for %s", w.Code)
	}

	prevBrokers, prevTotal, _ := c.repo.GetPrevHKBrokers(ctx, padHKCode(w.Code), settleDate)
	px := q.VWAP
	if px <= 0 {
		px = q.Close
	}

	totalShares := 0.0
	for i := range brokers {
		totalShares += brokers[i].Shares
		if prevSh, ok := prevBrokers[brokers[i].ParticipantID]; ok && prevSh > 0 {
			brokers[i].ShareDelta = brokers[i].Shares - prevSh
			brokers[i].FlowHKDYi = (brokers[i].ShareDelta * px) / 1e8
		}
	}
	totalDelta := 0.0
	if prevTotal > 0 {
		totalDelta = totalShares - prevTotal
	}

	anomFlag, anomNote, transferIDs := DetectCCASSAnomalies(brokers, totalDelta, q.Volume)
	snap := HKCCASSSnapshot{
		Code:             padHKCode(w.Code),
		SettlementDate:   settleDate,
		TradeDate:        SettlementToTradeDate(settleDate),
		TotalCCASSShares: totalShares,
		AnomalyFlag:      anomFlag,
		AnomalyNote:      anomNote,
		SourceURL:        ccURL,
	}
	for i := range brokers {
		if transferIDs[brokers[i].ParticipantID] {
			brokers[i].TransferFlag = "CUSTODIAN_TRANSFER"
		}
		switch brokers[i].Cohort {
		case "southbound":
			snap.SouthboundShares += brokers[i].Shares
			snap.SouthboundFlowYi += brokers[i].FlowHKDYi
		case "foreign_inst":
			snap.ForeignShares += brokers[i].Shares
			snap.ForeignRawFlowYi += brokers[i].FlowHKDYi
			if brokers[i].TransferFlag == "" && anomFlag != "ADR_CONVERSION" {
				snap.ForeignCleanFlowYi += brokers[i].FlowHKDYi
			}
		case "cn_inst":
			snap.CNInstShares += brokers[i].Shares
			snap.CNInstFlowYi += brokers[i].FlowHKDYi
		case "internet_retail":
			snap.InternetShares += brokers[i].Shares
			snap.InternetFlowYi += brokers[i].FlowHKDYi
		}
	}

	// 保留前 25 大单家席位，并确保富途(B01955)与盈透(B01590)只要有持仓必然保留
	sort.Slice(brokers, func(i, j int) bool { return brokers[i].Shares > brokers[j].Shares })
	kept := make([]HKBrokerPosition, 0, 27)
	seen := map[string]bool{}
	for i, b := range brokers {
		if i < 25 || b.ParticipantID == "B01955" || b.ParticipantID == "B01590" {
			if !seen[b.ParticipantID] {
				seen[b.ParticipantID] = true
				kept = append(kept, b)
			}
		}
	}
	return c.repo.SaveHKCCASSSnapshot(ctx, snap, kept)
}

func (c *HKCollector) getHTML(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("hkex http %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	return string(raw), err
}

func (c *HKCollector) fetchMutualMarketForDate(ctx context.Context, sbURL, initHTML, targetDate string) (string, []rawSBItem, error) {
	form := extractAspNetHiddenFields(initHTML)
	form.Set("txtShareholdingDate", strings.ReplaceAll(targetDate, "-", "/"))
	form.Set("btnSearch", "搜尋")
	form.Set("__EVENTTARGET", "btnSearch")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sbURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", sbURL)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	dt, items := parseMutualMarketHTML(string(raw))
	return dt, items, nil
}

type rawSBItem struct {
	Code     string
	Name     string
	Shares   float64
	RatioPct float64
}

var (
	hkDateRe    = regexp.MustCompile(`(\d{4})/(\d{2})/(\d{2})`)
	trBlockRe   = regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
	tdValRe     = regexp.MustCompile(`(?is)<div[^>]*class="mobile-list-body"[^>]*>(.*?)</div>`)
	hiddenInpRe = regexp.MustCompile(`(?is)<input[^>]+type="hidden"[^>]+name="([^"]+)"[^>]+value="([^"]*)"`)
	htmlStripRe = regexp.MustCompile(`<[^>]+>`)
)

func parseMutualMarketHTML(body string) (string, []rawSBItem) {
	settleDate := ""
	if idx := strings.Index(body, "txtShareholdingDate"); idx > 0 {
		slice := body[idx:minInt(len(body), idx+260)]
		if m := hkDateRe.FindStringSubmatch(slice); len(m) == 4 {
			settleDate = fmt.Sprintf("%s-%s-%s", m[1], m[2], m[3])
		}
	}
	if settleDate == "" {
		if m := hkDateRe.FindStringSubmatch(body); len(m) == 4 {
			settleDate = fmt.Sprintf("%s-%s-%s", m[1], m[2], m[3])
		}
	}
	var items []rawSBItem
	for _, tr := range trBlockRe.FindAllStringSubmatch(body, -1) {
		cols := tdValRe.FindAllStringSubmatch(tr[1], -1)
		if len(cols) < 4 {
			continue
		}
		code := cleanCell(cols[0][1])
		name := cleanCell(cols[1][1])
		shStr := strings.ReplaceAll(cleanCell(cols[2][1]), ",", "")
		pctStr := strings.TrimSuffix(cleanCell(cols[3][1]), "%")
		sh, err := strconv.ParseFloat(shStr, 64)
		if err != nil || sh <= 0 || code == "" {
			continue
		}
		pct, _ := strconv.ParseFloat(pctStr, 64)
		items = append(items, rawSBItem{Code: padHKCode(code), Name: name, Shares: sh, RatioPct: pct})
	}
	return settleDate, items
}

func parseCCASSHTML(body, code, settleDate string) []HKBrokerPosition {
	var list []HKBrokerPosition
	for _, tr := range trBlockRe.FindAllStringSubmatch(body, -1) {
		cols := tdValRe.FindAllStringSubmatch(tr[1], -1)
		if len(cols) < 4 {
			continue
		}
		pid := strings.ToUpper(cleanCell(cols[0][1]))
		pname := cleanCell(cols[1][1])
		// CCASS 表格含地址列时，股数在倒数第 2 列、占比在最后 1 列
		shStr := strings.ReplaceAll(cleanCell(cols[len(cols)-2][1]), ",", "")
		pctStr := strings.TrimSuffix(cleanCell(cols[len(cols)-1][1]), "%")
		sh, err := strconv.ParseFloat(shStr, 64)
		if err != nil || sh <= 0 || pname == "" {
			continue
		}
		if pid == "" {
			pid = "NON-" + strings.ReplaceAll(pname, " ", "")
			if len(pid) > 20 {
				pid = pid[:20]
			}
		}
		pct, _ := strconv.ParseFloat(pctStr, 64)
		list = append(list, HKBrokerPosition{
			Code:            code,
			SettlementDate:  settleDate,
			ParticipantID:   pid,
			ParticipantName: pname,
			Cohort:          ClassifyHKBrokerCohort(pid, pname),
			Shares:          sh,
			HoldRatioPct:    pct,
		})
	}
	return list
}

func extractAspNetHiddenFields(body string) url.Values {
	vals := url.Values{}
	for _, m := range hiddenInpRe.FindAllStringSubmatch(body, -1) {
		vals.Set(m[1], html.UnescapeString(m[2]))
	}
	return vals
}

func cleanCell(s string) string {
	return strings.TrimSpace(html.UnescapeString(htmlStripRe.ReplaceAllString(s, "")))
}

func prevBusinessDay(dateStr string) string {
	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return dateStr
	}
	for {
		t = t.AddDate(0, 0, -1)
		if t.Weekday() != time.Saturday && t.Weekday() != time.Sunday {
			return t.Format("2006-01-02")
		}
	}
}

func watchCodes(list []WatchItem) []string {
	out := make([]string, 0, len(list))
	for _, w := range list {
		out = append(out, w.Code)
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
