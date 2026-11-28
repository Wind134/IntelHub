package capital

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	"intel-hub/internal/model"
)

// AlertSink 复用 intel-hub 统一通知管道（SQLite 归档 + WebSocket 客户端 + Android FCM + 飞书卡片）。
type AlertSink func(ctx context.Context, ev *model.IntelEvent)

// IsCoreBroadIndexETF 判断是否属于国家队核心宽基 ETF（排除行业主题类，避免盘中噪声推送）。
func IsCoreBroadIndexETF(category string) bool {
	switch strings.TrimSpace(category) {
	case "沪深300", "上证50", "中证500", "中证1000", "创业板", "科创板", "上证180", "深证100", "中证A500", "中证2000":
		return true
	default:
		return false
	}
}

func (c *CNCollector) alertSent(ctx context.Context, key string) bool {
	v, err := c.repo.st.GetSetting(ctx, key)
	return err == nil && v == "1"
}

func (c *CNCollector) markAlertSent(ctx context.Context, key string) {
	_ = c.repo.st.SetSetting(ctx, key, "1")
}

// maybeEmitIntradayAlert 盘中核心宽基放量预警（>=2.5x 首次推，>=5.0x 极端再补推 1 次，成交额 < 5亿过滤）。
func (c *CNCollector) maybeEmitIntradayAlert(ctx context.Context, w WatchItem, row CNETFDaily) {
	if c.onAlert == nil || !IsCoreBroadIndexETF(w.Category) {
		return
	}
	if !row.IsIntraday || row.VolumeSignal != "intraday_surge" || row.VolRatio20 < 2.5 || row.TurnoverRMB < 5e8 {
		return
	}

	level := 1
	score := 9
	tag := "异常放量"
	if row.VolRatio20 >= 5.0 {
		level = 2
		score = 10
		tag = "极端放量"
	}

	dedupKey := fmt.Sprintf("cap_alert:%s:%s:intra%d", row.TradeDate, w.Code, level)
	if c.alertSent(ctx, dedupKey) {
		return
	}
	// 若直接跳到 level 2，同时标记 level 1 已发送避免回落补发
	if level == 2 {
		c.markAlertSent(ctx, fmt.Sprintf("cap_alert:%s:%s:intra1", row.TradeDate, w.Code))
	}
	c.markAlertSent(ctx, dedupKey)

	toYi := row.TurnoverRMB / 1e8
	title := fmt.Sprintf("【A股宽基盘中%s】%s(%s) 折算量比 %.1fx", tag, w.Name, w.Code, row.VolRatio20)
	summary := fmt.Sprintf("当前成交额 ¥%.2f亿（折算量比 %.2fx），现价 ¥%.3f，疑似大资金盘中显著放量进场，具体净申赎待 19:30 交易所官方份额定性。", toYi, row.VolRatio20, row.ClosePrice)
	rawMD := fmt.Sprintf(
		"- **标的**：%s（`%s.%s` · %s）\n- **实时折算量比**：**%.2fx**（阈值 ≥ 2.5x）\n- **当前成交额**：¥%.2f 亿元\n- **最新价格**：¥%.4f\n- **说明**：盘中放量为阶段一实时观测信号，19:30 盘后将叠加官方份额变动定性是否为真实净申购。",
		w.Name, w.Code, w.Exchange, w.Category, row.VolRatio20, toYi, row.ClosePrice,
	)

	ev := &model.IntelEvent{
		EventCode:          fmt.Sprintf("CAP-CN-%s-%s-L%d", strings.ReplaceAll(row.TradeDate, "-", ""), w.Code, level),
		Title:              title,
		Summary:            summary,
		RawContent:         rawMD,
		SourceName:         "三地资金流向看板 · A股盘中监控",
		SourceType:         "capital_cn_intraday",
		Category:           "finance",
		Score:              score,
		Tier:               "P1",
		Confidence:         0.92,
		Entities:           []string{w.Name, w.Code, w.Category, "国家队ETF"},
		ImpactNote:         "A股国家队核心宽基 ETF 盘中显著放量预警",
		VerificationStatus: "confirmed",
		VerificationNote:   "盘中实时成交额按 m/240 折算量比触发",
		PublishedAt:        time.Now().Unix(),
		PushStatus:         "pushed",
	}
	log.Printf("[capital:alert] emitting intraday alert: %s", title)
	c.onAlert(ctx, ev)
}

// maybeEmitEODSummaryAlert 盘后核心宽基大额净申赎汇总推送（单日合计 >= 30亿 / 单只 >= 15亿 / 有确认放量净申购时，每日仅推 1 条）。
func (c *CNCollector) maybeEmitEODSummaryAlert(ctx context.Context, todayBJ string) {
	if c.onAlert == nil {
		return
	}
	summaries, err := c.repo.ListCNETFSummaries(ctx)
	if err != nil || len(summaries) == 0 {
		return
	}

	var core []CNETFSummary
	totalFlowYi := 0.0
	maxAbsYi := 0.0
	var confirmedNames []string
	for _, s := range summaries {
		if !IsCoreBroadIndexETF(s.Category) || s.IsIntraday || s.LatestDate != todayBJ {
			continue
		}
		if math.Abs(s.Flow1DYi) < 0.0001 {
			continue
		}
		core = append(core, s)
		totalFlowYi += s.Flow1DYi
		if math.Abs(s.Flow1DYi) > maxAbsYi {
			maxAbsYi = math.Abs(s.Flow1DYi)
		}
		if s.VolumeSignal == "confirmed_inflow" {
			confirmedNames = append(confirmedNames, fmt.Sprintf("%s(%s +¥%.1f亿/%.1fx)", s.Name, s.Code, s.Flow1DYi, s.VolRatio20))
		}
	}
	if len(core) == 0 {
		return
	}
	if math.Abs(totalFlowYi) < 30.0 && maxAbsYi < 15.0 && len(confirmedNames) == 0 {
		return
	}

	dedupKey := fmt.Sprintf("cap_alert:%s:eod_summary", todayBJ)
	if c.alertSent(ctx, dedupKey) {
		return
	}
	c.markAlertSent(ctx, dedupKey)

	sort.Slice(core, func(i, j int) bool {
		return math.Abs(core[i].Flow1DYi) > math.Abs(core[j].Flow1DYi)
	})
	topN := len(core)
	if topN > 6 {
		topN = 6
	}

	dirLabel := "净申购"
	sign := "+"
	if totalFlowYi < 0 {
		dirLabel = "净赎回"
		sign = ""
	}
	title := fmt.Sprintf("【A股核心宽基盘后简报】%s合计%s %s¥%.1f亿", todayBJ, dirLabel, sign, totalFlowYi)

	var lines []string
	var briefParts []string
	for _, s := range core[:topN] {
		sSign := "+"
		if s.Flow1DYi < 0 {
			sSign = ""
		}
		lines = append(lines, fmt.Sprintf("- **%s（%s）**：%s¥%.2f亿（量比 %.2fx）", s.Name, s.Code, sSign, s.Flow1DYi, s.VolRatio20))
		if len(briefParts) < 3 {
			briefParts = append(briefParts, fmt.Sprintf("%s %s¥%.1f亿", s.Name, sSign, s.Flow1DYi))
		}
	}
	summary := fmt.Sprintf("国家队核心宽基池当日合计%s %s¥%.2f亿；主力前排：%s。", dirLabel, sign, totalFlowYi, strings.Join(briefParts, "、"))
	if len(confirmedNames) > 0 {
		summary += " 盘后确认放量净申购：" + strings.Join(confirmedNames, "、") + "。"
	}

	rawMD := fmt.Sprintf("### %s 国家队核心宽基盘后份额定性（三源核验）\n- **核心宽基池日净申赎**：**%s¥%.2f 亿元**\n", todayBJ, sign, totalFlowYi)
	if len(confirmedNames) > 0 {
		rawMD += fmt.Sprintf("- **确认放量净申购**：%s\n", strings.Join(confirmedNames, "、"))
	}
	rawMD += "\n**当日变动前排明细（RMB）**：\n" + strings.Join(lines, "\n")

	ev := &model.IntelEvent{
		EventCode:          fmt.Sprintf("CAP-CN-EOD-%s", strings.ReplaceAll(todayBJ, "-", "")),
		Title:              title,
		Summary:            summary,
		RawContent:         rawMD,
		SourceName:         "三地资金流向看板 · 盘后三源定性",
		SourceType:         "capital_cn_eod",
		Category:           "finance",
		Score:              9,
		Tier:               "P1",
		Confidence:         0.98,
		Entities:           []string{"国家队ETF", "沪深300", "中证500", "中证1000"},
		ImpactNote:         "A股国家队核心宽基 ETF 盘后官方份额大额净申赎定性简报",
		VerificationStatus: "confirmed",
		VerificationNote:   "上交所官方 + Tushare Pro fund_share 三源核验",
		PublishedAt:        time.Now().Unix(),
		PushStatus:         "pushed",
	}
	log.Printf("[capital:alert] emitting EOD summary alert: %s", title)
	c.onAlert(ctx, ev)
}
