package capital

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// USCollector 直连美国证监会 SEC EDGAR 官方 API (data.sec.gov)，解析 13F-HR 原始 XML。
type USCollector struct {
	repo       *Repo
	userAgent  string
	http       *http.Client
	onProgress ProgressFunc

	mu          sync.Mutex
	lastReq     time.Time
	name2Ticker map[string]string
}

func (c *USCollector) report(cur, total int, stage string) {
	if c.onProgress != nil {
		c.onProgress(cur, total, stage)
	}
}

func NewUSCollector(repo *Repo, userAgent string) *USCollector {
	ua := strings.TrimSpace(userAgent)
	if ua == "" {
		ua = "InformationHubResearch/1.0 (research@informationhub.app)"
	}
	return &USCollector{
		repo:      repo,
		userAgent: ua,
		http:      &http.Client{Timeout: 25 * time.Second},
	}
}

// secGet 合规限速（<= 4 req/s，严格满足 SEC 10 req/s 红线）并携带合规 User-Agent。
func (c *USCollector) secGet(ctx context.Context, rawURL string) ([]byte, error) {
	c.mu.Lock()
	wait := 260*time.Millisecond - time.Since(c.lastReq)
	if wait > 0 {
		time.Sleep(wait)
	}
	c.lastReq = time.Now()
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("sec http %d for %s", resp.StatusCode, rawURL)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 24*1024*1024))
}

func (c *USCollector) ensureTickerMap(ctx context.Context) {
	c.mu.Lock()
	ready := len(c.name2Ticker) > 0
	c.mu.Unlock()
	if ready {
		return
	}
	raw, err := c.secGet(ctx, "https://www.sec.gov/files/company_tickers.json")
	if err != nil {
		return
	}
	var entries map[string]struct {
		Ticker string `json:"ticker"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return
	}
	m := make(map[string]string, len(entries)*2)
	for _, e := range entries {
		tk := strings.ToUpper(strings.TrimSpace(e.Ticker))
		if tk == "" {
			continue
		}
		norm := normalizeIssuerName(e.Title)
		if norm != "" {
			if _, exists := m[norm]; !exists {
				m[norm] = tk
			}
		}
	}
	// 常用高频 13F 缩写别名补充
	aliases := map[string]string{
		"APPLE": "AAPL", "MICROSOFT": "MSFT", "NVIDIA": "NVDA", "ALPHABET": "GOOGL",
		"AMAZON COM": "AMZN", "META PLATFORMS": "META", "TESLA": "TSLA",
		"TAIWAN SEMICONDUCTOR": "TSM", "BROADCOM": "AVGO", "BERKSHIRE HATHAWAY": "BRK-B",
		"BANK AMERICA": "BAC", "AMERICAN EXPRESS": "AXP", "COCA COLA": "KO",
		"CHEVRON": "CVX", "OCCIDENTAL PETE": "OXY", "MOODYS": "MCO", "KRAFT HEINZ": "KHC",
		"CHUBB": "CB", "ALIBABA GROUP": "BABA", "PDD": "PDD", "JD COM": "JD",
		"NETFLIX": "NFLX", "ELI LILLY": "LLY", "JPMORGAN CHASE": "JPM",
		"UNITEDHEALTH": "UNH", "VISA": "V", "MASTERCARD": "MA", "ORACLE": "ORCL",
		"ADVANCED MICRO DEVICES": "AMD", "PALANTIR": "PLTR", "SPDR S&P 500": "SPY",
		"INVESCO QQQ": "QQQ", "ISHARES CORE S&P 500": "IVV",
	}
	for k, v := range aliases {
		m[k] = v
	}
	c.mu.Lock()
	c.name2Ticker = m
	c.mu.Unlock()
}

func (c *USCollector) lookupTicker(issuer string) string {
	c.mu.Lock()
	m := c.name2Ticker
	c.mu.Unlock()
	if len(m) == 0 {
		return ""
	}
	norm := normalizeIssuerName(issuer)
	if tk, ok := m[norm]; ok {
		return tk
	}
	// 尝试前缀两词匹配（如 "NVIDIA CORP" -> "NVIDIA"）
	parts := strings.Fields(norm)
	if len(parts) >= 2 {
		if tk, ok := m[parts[0]+" "+parts[1]]; ok {
			return tk
		}
	}
	if len(parts) >= 1 {
		if tk, ok := m[parts[0]]; ok {
			return tk
		}
	}
	return ""
}

func normalizeIssuerName(s string) string {
	s = strings.ToUpper(s)
	replacer := strings.NewReplacer(".", " ", ",", " ", "-", " ", "/", " ", "&", " ", "'", "")
	s = replacer.Replace(s)
	stop := map[string]bool{
		"INC": true, "CORP": true, "CORPORATION": true, "CO": true, "LTD": true,
		"LIMITED": true, "PLC": true, "NV": true, "SA": true, "AG": true,
		"HOLDINGS": true, "HOLDING": true, "HLDGS": true, "CL": true, "CLASS": true,
		"COM": true, "NEW": true, "DEL": true, "THE": true, "GROUP": true, "GRP": true,
		"SPONSORED": true, "ADR": true, "ADS": true, "SHS": true,
	}
	var kept []string
	for _, f := range strings.Fields(s) {
		if stop[f] {
			continue
		}
		kept = append(kept, f)
	}
	return strings.Join(kept, " ")
}

type raw13FRow struct {
	Issuer     string
	CUSIP      string
	Value      float64
	Shares     float64
	AssetClass string // EQUITY | CALL | PUT
}

// Parse13FInfoTableXML 命名空间无关解析 SEC 13F-HR XML，严格隔离 CALL/PUT 期权并聚合相同 CUSIP。
func Parse13FInfoTableXML(raw []byte) ([]raw13FRow, error) {
	dec := xml.NewDecoder(strings.NewReader(string(raw)))
	var rows []raw13FRow
	var cur *raw13FRow
	var tagStack []string

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			lname := strings.ToLower(t.Name.Local)
			tagStack = append(tagStack, lname)
			if lname == "infotable" {
				cur = &raw13FRow{AssetClass: "EQUITY"}
			}
		case xml.CharData:
			if cur == nil || len(tagStack) == 0 {
				continue
			}
			val := strings.TrimSpace(string(t))
			if val == "" {
				continue
			}
			top := tagStack[len(tagStack)-1]
			switch top {
			case "nameofissuer":
				cur.Issuer = val
			case "cusip":
				cur.CUSIP = strings.ToUpper(val)
			case "value":
				if f, err := strconv.ParseFloat(strings.ReplaceAll(val, ",", ""), 64); err == nil {
					cur.Value = f
				}
			case "sshprnamt":
				if f, err := strconv.ParseFloat(strings.ReplaceAll(val, ",", ""), 64); err == nil {
					cur.Shares = f
				}
			case "putcall":
				up := strings.ToUpper(val)
				if strings.Contains(up, "CALL") {
					cur.AssetClass = "CALL"
				} else if strings.Contains(up, "PUT") {
					cur.AssetClass = "PUT"
				}
			}
		case xml.EndElement:
			lname := strings.ToLower(t.Name.Local)
			if lname == "infotable" && cur != nil {
				if cur.CUSIP != "" && (cur.Shares > 0 || cur.Value > 0) {
					rows = append(rows, *cur)
				}
				cur = nil
			}
			if len(tagStack) > 0 {
				tagStack = tagStack[:len(tagStack)-1]
			}
		}
	}

	// 同份报告内相同 CUSIP + AssetClass 合并求和（避免多子管理人拆行重复）
	agg := make(map[string]*raw13FRow, len(rows))
	for _, r := range rows {
		key := r.CUSIP + "|" + r.AssetClass
		if ex, ok := agg[key]; ok {
			ex.Shares += r.Shares
			ex.Value += r.Value
		} else {
			cp := r
			agg[key] = &cp
		}
	}
	out := make([]raw13FRow, 0, len(agg))
	for _, v := range agg {
		out = append(out, *v)
	}
	// 2023 年新旧规 $1 vs $1000 隐含股价量纲自动校验
	normalize13FValueScale(out)
	return out, nil
}

// normalize13FValueScale 通过正股中位数隐含股价自动识别历史“千美元”申报，彻底杜绝 1000 倍量纲乌龙。
func normalize13FValueScale(rows []raw13FRow) {
	var implied []float64
	for _, r := range rows {
		if r.AssetClass == "EQUITY" && r.Shares >= 100 && r.Value > 0 {
			implied = append(implied, r.Value/r.Shares)
		}
	}
	if len(implied) == 0 {
		return
	}
	sort.Float64s(implied)
	med := implied[len(implied)/2]
	// 若正股中位数隐含股价 < $0.50，几乎必为旧版“千美元”单位
	if med > 0 && med < 0.50 {
		for i := range rows {
			rows[i].Value *= 1000.0
		}
	}
}

// Classify13FAction 判定 13F 调仓动作五分类及估算季度资金流（含拆股因子校准）。
func Classify13FAction(prevShares, curShares, splitFactor, quarterVWAP float64) (prevAdj, delta, changePct, estFlow float64, action string) {
	if splitFactor <= 0 {
		splitFactor = 1.0
	}
	prevAdj = prevShares * splitFactor
	delta = curShares - prevAdj
	switch {
	case prevAdj <= 0 && curShares > 0:
		action = "NEW"
		changePct = 100.0
	case prevAdj > 0 && curShares <= 0:
		action = "SOLD_OUT"
		changePct = -100.0
	case math.Abs(delta) < 1:
		action = "HOLD"
		delta = 0
	case delta > 0:
		action = "ADD"
		changePct = (delta / prevAdj) * 100.0
	default:
		action = "REDUCE"
		changePct = (delta / prevAdj) * 100.0
	}
	estFlow = delta * quarterVWAP
	return
}

// SyncAll 同步全部跟踪美股 13F 机构最近 2 个季度申报，计算真实增减持流向。
func (c *USCollector) SyncAll(ctx context.Context) error {
	watches, err := c.repo.ListWatchlist(ctx, "us_inst")
	if err != nil {
		return err
	}
	c.report(5, 100, "加载 SEC 证券代码映射表...")
	c.ensureTickerMap(ctx)

	synced := 0
	latestReport := ""
	for idx, w := range watches {
		if ctx.Err() != nil {
			break
		}
		c.report(10+((idx+1)*88)/maxInt(1, len(watches)), 100, fmt.Sprintf("解析 SEC 13F %s (%d/%d)", w.Name, idx+1, len(watches)))
		repDate, err := c.syncOneInstitution(ctx, w)
		if err != nil {
			log.Printf("[capital:us] sync %s (%s) warn: %v", w.Code, w.Name, err)
			continue
		}
		if repDate > latestReport {
			latestReport = repDate
		}
		synced++
	}
	c.repo.SetSyncStatus(ctx, SyncStatus{
		Market:      "us_13f",
		Status:      "ok",
		DataAsOf:    latestReport,
		ItemCount:   synced,
		SourceLabel: "美国证监会 SEC EDGAR 官方 13F-HR XML",
	})
	return nil
}

type secFilingMeta struct {
	Accession  string
	ReportDate string
	FilingDate string
}

func (c *USCollector) syncOneInstitution(ctx context.Context, w WatchItem) (string, error) {
	cik10 := padCIK(w.Code)
	cikNum := strings.TrimLeft(cik10, "0")
	subURL := fmt.Sprintf("https://data.sec.gov/submissions/CIK%s.json", cik10)
	raw, err := c.secGet(ctx, subURL)
	if err != nil {
		return "", err
	}
	var sub struct {
		Name    string `json:"name"`
		Filings struct {
			Recent struct {
				Form            []string `json:"form"`
				AccessionNumber []string `json:"accessionNumber"`
				FilingDate      []string `json:"filingDate"`
				ReportDate      []string `json:"reportDate"`
			} `json:"recent"`
		} `json:"filings"`
	}
	if err := json.Unmarshal(raw, &sub); err != nil {
		return "", err
	}

	// 取最近 2 个不同报告期的 13F-HR（用于精确计算当季 vs 上季变动）
	var targets []secFilingMeta
	seenRep := map[string]bool{}
	rec := sub.Filings.Recent
	for i := 0; i < len(rec.Form) && len(targets) < 2; i++ {
		if rec.Form[i] != "13F-HR" {
			continue
		}
		rd := rec.ReportDate[i]
		if rd == "" || seenRep[rd] {
			continue
		}
		seenRep[rd] = true
		targets = append(targets, secFilingMeta{
			Accession:  rec.AccessionNumber[i],
			ReportDate: rd,
			FilingDate: rec.FilingDate[i],
		})
	}
	if len(targets) == 0 {
		return "", fmt.Errorf("no 13F-HR filings found")
	}

	// 解析当前期与上一期 XML
	curRows, curURL, err := c.fetchAndParse13F(ctx, cikNum, targets[0].Accession)
	if err != nil {
		return "", err
	}
	var prevRows []raw13FRow
	var prevURL string
	if len(targets) >= 2 {
		prevRows, prevURL, _ = c.fetchAndParse13F(ctx, cikNum, targets[1].Accession)
	}

	// 先保存上一期基线（若有）
	if len(prevRows) > 0 {
		prevFiling, prevHoldings := c.buildHoldings(w, targets[1], prevURL, prevRows, nil)
		_ = c.repo.SaveUS13FFiling(ctx, prevFiling, prevHoldings)
	}

	curFiling, curHoldings := c.buildHoldings(w, targets[0], curURL, curRows, prevRows)
	if err := c.repo.SaveUS13FFiling(ctx, curFiling, curHoldings); err != nil {
		return "", err
	}
	return targets[0].ReportDate, nil
}

func (c *USCollector) fetchAndParse13F(ctx context.Context, cikNum, accession string) ([]raw13FRow, string, error) {
	accNoDash := strings.ReplaceAll(accession, "-", "")
	idxURL := fmt.Sprintf("https://www.sec.gov/Archives/edgar/data/%s/%s/index.json", cikNum, accNoDash)
	rawIdx, err := c.secGet(ctx, idxURL)
	if err != nil {
		return nil, "", err
	}
	var idx struct {
		Directory struct {
			Item []struct {
				Name string `json:"name"`
				Size string `json:"size"`
			} `json:"item"`
		} `json:"directory"`
	}
	if err := json.Unmarshal(rawIdx, &idx); err != nil {
		return nil, "", err
	}

	// 挑选 infotable XML（排除 primary_doc.xml，按文件体积最大匹配真实持仓明细表）
	bestFile := ""
	bestSize := int64(-1)
	for _, it := range idx.Directory.Item {
		low := strings.ToLower(it.Name)
		if !strings.HasSuffix(low, ".xml") || strings.Contains(low, "primary_doc") || strings.Contains(low, "xsl") {
			continue
		}
		sz, _ := strconv.ParseInt(strings.TrimSpace(it.Size), 10, 64)
		if strings.Contains(low, "info") {
			sz += 100000000 // 优先匹配名称含 info 的 XML
		}
		if sz > bestSize {
			bestSize = sz
			bestFile = it.Name
		}
	}
	if bestFile == "" {
		return nil, "", fmt.Errorf("no 13F infotable xml in %s", accession)
	}
	xmlURL := fmt.Sprintf("https://www.sec.gov/Archives/edgar/data/%s/%s/%s", cikNum, accNoDash, bestFile)
	xmlBytes, err := c.secGet(ctx, xmlURL)
	if err != nil {
		return nil, xmlURL, err
	}
	rows, err := Parse13FInfoTableXML(xmlBytes)
	return rows, xmlURL, err
}

func (c *USCollector) buildHoldings(w WatchItem, meta secFilingMeta, srcURL string, curRows, prevRows []raw13FRow) (US13FFiling, []US13FHolding) {
	prevMap := make(map[string]raw13FRow, len(prevRows))
	for _, r := range prevRows {
		prevMap[r.CUSIP+"|"+r.AssetClass] = r
	}
	curMap := make(map[string]raw13FRow, len(curRows))
	totalEquityVal := 0.0
	eqCount := 0
	optCount := 0
	for _, r := range curRows {
		curMap[r.CUSIP+"|"+r.AssetClass] = r
		if r.AssetClass == "EQUITY" {
			totalEquityVal += r.Value
			eqCount++
		} else {
			optCount++
		}
	}

	keys := make(map[string]bool, len(curMap)+len(prevMap))
	for k := range curMap {
		keys[k] = true
	}
	for k := range prevMap {
		keys[k] = true
	}

	holdings := make([]US13FHolding, 0, len(keys))
	for k := range keys {
		cur, hasCur := curMap[k]
		prev, hasPrev := prevMap[k]
		ref := cur
		if !hasCur {
			ref = prev
		}

		curPrice := 0.0
		if hasCur && cur.Shares > 0 {
			curPrice = cur.Value / cur.Shares
		}
		prevPrice := 0.0
		if hasPrev && prev.Shares > 0 {
			prevPrice = prev.Value / prev.Shares
		}

		// 自动检测季内常见整数拆股（2/3/4/5/10 拆 1），避免把拆股误判成巨额加仓
		splitFactor := 1.0
		if hasCur && hasPrev && prevPrice > 0 && curPrice > 0 && cur.AssetClass == "EQUITY" {
			pRatio := prevPrice / curPrice
			sRatio := cur.Shares / prev.Shares
			for _, candidate := range []float64{2, 3, 4, 5, 10, 20} {
				if math.Abs(pRatio-candidate)/candidate < 0.28 && math.Abs(sRatio-candidate)/candidate < 0.35 {
					splitFactor = candidate
					prevPrice = prevPrice / candidate
					break
				}
			}
		}

		qVWAP := curPrice
		if curPrice > 0 && prevPrice > 0 {
			qVWAP = 0.5 * (curPrice + prevPrice)
		} else if qVWAP <= 0 {
			qVWAP = prevPrice
		}

		prevAdj, delta, chgPct, estFlow, action := Classify13FAction(prev.Shares, cur.Shares, splitFactor, qVWAP)
		wt := 0.0
		if ref.AssetClass == "EQUITY" && totalEquityVal > 0 {
			wt = (cur.Value / totalEquityVal) * 100.0
		}
		tk := c.lookupTicker(ref.Issuer)
		holdings = append(holdings, US13FHolding{
			CIK:             padCIK(w.Code),
			InstitutionName: w.Name,
			ReportDate:      meta.ReportDate,
			CUSIP:           ref.CUSIP,
			Ticker:          tk,
			IssuerName:      ref.Issuer,
			AssetClass:      ref.AssetClass,
			Shares:          cur.Shares,
			PrevSharesAdj:   prevAdj,
			ShareDelta:      delta,
			ChangePct:       chgPct,
			Action:          action,
			FilingValueUSD:  cur.Value,
			PortfolioWtPct:  wt,
			QuarterVWAP:     qVWAP,
			EstFlowUSD:      estFlow,
			LatestPriceUSD:  curPrice,
			MTMValueUSD:     cur.Value,
			SourceURL:       srcURL,
		})
	}

	// 大型资管（如先锋/贝莱德）持仓超 3000 只，保留正股前 120 大重仓 + 净流向绝对值前 60 大异动 + 期权前 30 大，确保轻量高速
	sort.Slice(holdings, func(i, j int) bool {
		return holdings[i].FilingValueUSD > holdings[j].FilingValueUSD
	})
	if len(holdings) > 220 {
		keep := make(map[string]US13FHolding, 220)
		eqAdded, optAdded := 0, 0
		for _, h := range holdings {
			key := h.CUSIP + "|" + h.AssetClass
			if h.AssetClass == "EQUITY" && eqAdded < 140 {
				keep[key] = h
				eqAdded++
			} else if h.AssetClass != "EQUITY" && optAdded < 30 {
				keep[key] = h
				optAdded++
			}
		}
		sort.Slice(holdings, func(i, j int) bool {
			return math.Abs(holdings[i].EstFlowUSD) > math.Abs(holdings[j].EstFlowUSD)
		})
		flowAdded := 0
		for _, h := range holdings {
			if h.AssetClass == "EQUITY" && flowAdded < 60 {
				keep[h.CUSIP+"|"+h.AssetClass] = h
				flowAdded++
			}
		}
		trimmed := make([]US13FHolding, 0, len(keep))
		for _, h := range keep {
			trimmed = append(trimmed, h)
		}
		holdings = trimmed
	}

	filing := US13FFiling{
		CIK:             padCIK(w.Code),
		InstitutionName: w.Name,
		Category:        w.Category,
		AccessionNumber: meta.Accession,
		ReportDate:      meta.ReportDate,
		FilingDate:      meta.FilingDate,
		TotalValueUSD:   totalEquityVal,
		PositionCount:   eqCount,
		OptionCount:     optCount,
		SourceURL:       srcURL,
	}
	return filing, holdings
}
