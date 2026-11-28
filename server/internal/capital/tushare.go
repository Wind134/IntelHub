package capital

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const tushareEndpoint = "https://api.tushare.pro"

// TushareClient 封装 Tushare Pro 标准 HTTP JSON API（不落盘、不打印 Token）。
type TushareClient struct {
	token string
	http  *http.Client
}

func NewTushareClient(token string) *TushareClient {
	return &TushareClient{
		token: strings.TrimSpace(token),
		http:  &http.Client{Timeout: 18 * time.Second},
	}
}

func (t *TushareClient) Enabled() bool {
	return t != nil && t.token != ""
}

type tushareResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Fields []string `json:"fields"`
		Items  [][]any  `json:"items"`
	} `json:"data"`
}

func parseTushareTable(fields []string, items [][]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, row := range items {
		m := make(map[string]any, len(fields))
		for i, f := range fields {
			if i < len(row) {
				m[f] = row[i]
			}
		}
		out = append(out, m)
	}
	return out
}

func (t *TushareClient) Query(ctx context.Context, apiName string, params map[string]string, fields string) ([]map[string]any, error) {
	if !t.Enabled() {
		return nil, fmt.Errorf("tushare token not configured")
	}
	body, _ := json.Marshal(map[string]any{
		"api_name": apiName,
		"token":    t.token,
		"params":   params,
		"fields":   fields,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tushareEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "InformationHub/1.5")

	resp, err := t.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var tr tushareResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8*1024*1024)).Decode(&tr); err != nil {
		return nil, err
	}
	if tr.Code != 0 {
		return nil, fmt.Errorf("tushare %s code=%d msg=%s", apiName, tr.Code, tr.Msg)
	}
	return parseTushareTable(tr.Data.Fields, tr.Data.Items), nil
}

// FetchFundShare 拉取单只沪深 ETF 指定区间的每日官方总份额（返回 YYYY-MM-DD -> 精确份数）。
func (t *TushareClient) FetchFundShare(ctx context.Context, tsCode, startDate, endDate string) (map[string]float64, error) {
	rows, err := t.Query(ctx, "fund_share", map[string]string{
		"ts_code":    tsCode,
		"start_date": toTSDate(startDate),
		"end_date":   toTSDate(endDate),
	}, "ts_code,trade_date,fd_share")
	if err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(rows))
	for _, r := range rows {
		dt := fromTSDate(toStr(r["trade_date"]))
		shWan := toFloat(r["fd_share"])
		if dt != "" && shWan > 0 {
			out[dt] = shWan * 10000.0
		}
	}
	return out, nil
}

// FetchFundNAV 拉取单只 ETF 指定区间的官方单位净值（返回 YYYY-MM-DD -> 单位净值元）。
func (t *TushareClient) FetchFundNAV(ctx context.Context, tsCode, startDate, endDate string) (map[string]float64, error) {
	rows, err := t.Query(ctx, "fund_nav", map[string]string{
		"ts_code":    tsCode,
		"start_date": toTSDate(startDate),
		"end_date":   toTSDate(endDate),
	}, "ts_code,nav_date,unit_nav")
	if err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(rows))
	for _, r := range rows {
		dt := fromTSDate(toStr(r["nav_date"]))
		nav := toFloat(r["unit_nav"])
		if dt != "" && nav > 0 {
			out[dt] = nav
		}
	}
	return out, nil
}

func toTSCode(code, exchange string) string {
	ex := strings.ToUpper(strings.TrimSpace(exchange))
	if ex != "SH" && ex != "SZ" {
		if strings.HasPrefix(code, "5") {
			ex = "SH"
		} else {
			ex = "SZ"
		}
	}
	return strings.TrimSpace(code) + "." + ex
}

func toTSDate(iso string) string {
	return strings.ReplaceAll(strings.TrimSpace(iso), "-", "")
}

func fromTSDate(yyyymmdd string) string {
	s := strings.TrimSpace(yyyymmdd)
	if len(s) == 8 {
		return s[0:4] + "-" + s[4:6] + "-" + s[6:8]
	}
	return s
}

func toStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f
	default:
		return 0
	}
}
