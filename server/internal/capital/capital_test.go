package capital

import (
	"math"
	"testing"
)

func TestCNTwoStageVolRatioAndSignal(t *testing.T) {
	prev := make([]float64, 20)
	for i := range prev {
		prev[i] = 100.0
	}
	// 盘中开盘 60 分钟（占全天 1/4），若成交额已达 75，实时量比应为 75 / (100 * 0.25) = 3.0
	vr := ComputeVolRatio20(75.0, prev, 60)
	if math.Abs(vr-3.0) > 1e-6 {
		t.Fatalf("expected intraday vol ratio 3.0, got %.4f", vr)
	}
	if sig := ClassifyVolumeSignal(vr, 0, true); sig != "intraday_surge" {
		t.Fatalf("expected intraday_surge, got %s", sig)
	}
	// 盘后定性：量比 >= 2.5 且净申购 > 0 -> confirmed_inflow；净申购 <= 0 -> high_turnover
	if sig := ClassifyVolumeSignal(2.8, 12.5, false); sig != "confirmed_inflow" {
		t.Fatalf("expected confirmed_inflow, got %s", sig)
	}
	if sig := ClassifyVolumeSignal(2.8, -1.2, false); sig != "high_turnover" {
		t.Fatalf("expected high_turnover, got %s", sig)
	}
}

func TestUS13FXMLIsolationAndUnitScale(t *testing.T) {
	sample := `<?xml version="1.0" encoding="UTF-8"?>
<informationTable xmlns="http://www.sec.gov/edgar/document/thirteenf/informationtable">
  <infoTable>
    <nameOfIssuer>NVIDIA CORP</nameOfIssuer>
    <cusip>67066G104</cusip>
    <value>14000000</value>
    <shrsOrPrnAmt><sshPrnamt>100000</sshPrnamt><sshPrnamtType>SH</sshPrnamtType></shrsOrPrnAmt>
  </infoTable>
  <infoTable>
    <nameOfIssuer>NVIDIA CORP</nameOfIssuer>
    <cusip>67066G104</cusip>
    <value>5000000</value>
    <shrsOrPrnAmt><sshPrnamt>50000</sshPrnamt><sshPrnamtType>SH</sshPrnamtType></shrsOrPrnAmt>
    <putCall>Call</putCall>
  </infoTable>
</informationTable>`

	rows, err := Parse13FInfoTableXML([]byte(sample))
	if err != nil {
		t.Fatalf("parse xml: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 isolated rows (EQUITY + CALL), got %d", len(rows))
	}
	for _, r := range rows {
		if r.AssetClass == "EQUITY" && r.Shares != 100000 {
			t.Fatalf("option shares leaked into equity: %+v", r)
		}
	}

	// 测试 1拆10 拆股校准与五分类判定
	prevAdj, delta, _, estFlow, act := Classify13FAction(1000, 12000, 10.0, 130.0)
	if prevAdj != 10000 || delta != 2000 || act != "ADD" || estFlow != 260000 {
		t.Fatalf("unexpected split-adjusted flow: prevAdj=%v delta=%v act=%s flow=%v", prevAdj, delta, act, estFlow)
	}
}

func TestHKCohortAndTransferDetection(t *testing.T) {
	if got := ClassifyHKBrokerCohort("B01955", "富途證券國際(香港)有限公司"); got != "internet_retail" {
		t.Fatalf("expected Futu B01955 -> internet_retail, got %s", got)
	}
	if got := ClassifyHKBrokerCohort("B01590", "INTERACTIVE BROKERS HONG KONG LTD"); got != "internet_retail" {
		t.Fatalf("expected IBKR B01590 -> internet_retail, got %s", got)
	}
	if got := ClassifyHKBrokerCohort("C00019", "中國證券登記結算有限責任公司"); got != "southbound" {
		t.Fatalf("expected C00019 -> southbound, got %s", got)
	}
	if got := ClassifyHKBrokerCohort("C00001", "THE HONGKONG AND SHANGHAI BANKING CORP"); got != "foreign_inst" {
		t.Fatalf("expected HSBC -> foreign_inst, got %s", got)
	}

	// T+2 交收日回推（2026-11-30 周一 -> 前推 2 个工作日为 2026-11-26 周四）
	if td := SettlementToTradeDate("2026-11-30"); td != "2026-11-26" {
		t.Fatalf("expected T+2 trade date 2026-11-26, got %s", td)
	}

	// 跨席位转仓检测：汇丰转出 1000 万股，花旗转入 990 万股，当日市场总成交 1200 万股
	brokers := []HKBrokerPosition{
		{ParticipantID: "C00001", ParticipantName: "汇丰", Cohort: "foreign_inst", ShareDelta: -10000000},
		{ParticipantID: "C00010", ParticipantName: "花旗", Cohort: "foreign_inst", ShareDelta: 9900000},
	}
	flag, _, ids := DetectCCASSAnomalies(brokers, -100000, 12000000)
	if flag != "CUSTODIAN_TRANSFER" || !ids["C00001"] || !ids["C00010"] {
		t.Fatalf("expected CUSTODIAN_TRANSFER detection, got flag=%s ids=%v", flag, ids)
	}
}

func TestTushareHelpers(t *testing.T) {
	if got := toTSCode("159919", "SZ"); got != "159919.SZ" {
		t.Fatalf("unexpected ts code: %s", got)
	}
	if got := fromTSDate("20261127"); got != "2026-11-27" {
		t.Fatalf("unexpected date: %s", got)
	}
	rows := parseTushareTable(
		[]string{"ts_code", "trade_date", "fd_share"},
		[][]any{{"159919.SZ", "20261127", 2133201.66}},
	)
	if len(rows) != 1 || toFloat(rows[0]["fd_share"]) != 2133201.66 {
		t.Fatalf("unexpected parsed rows: %+v", rows)
	}
}
