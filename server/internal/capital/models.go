package capital

// 数据真实性等级常量
const (
	ReliabilityExact        = "EXACT"
	ReliabilityOfficialFlow = "OFFICIAL_FLOW"
	ReliabilityEstFlow      = "EST_FLOW"
)

type WatchItem struct {
	ID       int64  `json:"id"`
	Market   string `json:"market"`    // cn_etf | us_inst | hk_stock
	Code     string `json:"code"`      // 510300 | 0001067983 | 00700
	Name     string `json:"name"`
	Category string `json:"category"`  // 沪深300 | 价值长线 | 科技龙头 等
	Exchange string `json:"exchange"`  // SH | SZ | US | HK
	Enabled  bool   `json:"enabled"`
}

type CNETFDaily struct {
	Code            string  `json:"code"`
	TradeDate       string  `json:"trade_date"`
	TotalShares     float64 `json:"total_shares"`       // 精确总份额（份）
	ShareDelta      float64 `json:"share_delta"`        // 当日份额变动（份）
	NAV             float64 `json:"nav"`                // 官方单位净值（元/份）
	ClosePrice      float64 `json:"close_price"`        // 二级市场收盘价（元）
	TurnoverRMB     float64 `json:"turnover_rmb"`       // 当日成交额（元）
	NetFlowRMB      float64 `json:"net_flow_rmb"`       // 当日一级市场净申赎流向（亿元 RMB）
	VolRatio20      float64 `json:"vol_ratio_20"`       // 20日量比（盘中按 m/240 折算，盘后为日终值）
	VolumeSignal    string  `json:"volume_signal"`      // normal | intraday_surge | confirmed_inflow | high_turnover
	IsIntraday      bool    `json:"is_intraday"`
	VerifyStatus    string  `json:"verify_status"`      // VERIFIED | SINGLE_SOURCE | WARN
	SourceNote      string  `json:"source_note"`
	PrimaryShares   float64 `json:"primary_shares"`
	BackupShares    float64 `json:"backup_shares"`
	UpdatedAt       int64   `json:"updated_at"`
}

type CNETFHolder struct {
	Code          string  `json:"code"`
	ReportDate    string  `json:"report_date"`    // 2026-09-30 / 2026-06-30
	ReportType    string  `json:"report_type"`    // 三季报(>=20%) | 中报 | 年报 | 增持公告
	HolderName    string  `json:"holder_name"`
	SharesHeld    float64 `json:"shares_held"`    // 精确持有份额（份）
	HoldRatioPct  float64 `json:"hold_ratio_pct"` // 占总份额比例（%）
	FilingValueYi float64 `json:"filing_value_yi"`// 报告期末法定市值（亿元 RMB）
	SourceURL     string  `json:"source_url"`
	UpdatedAt     int64   `json:"updated_at"`
}

type CNETFSummary struct {
	Code             string        `json:"code"`
	Name             string        `json:"name"`
	Category         string        `json:"category"`
	Exchange         string        `json:"exchange"`
	LatestDate       string        `json:"latest_date"`
	NAV              float64       `json:"nav"`
	ClosePrice       float64       `json:"close_price"`
	TotalShares      float64       `json:"total_shares"`
	TotalAUMYi       float64       `json:"total_aum_yi"`       // 总规模（亿元 RMB）
	Flow1DYi         float64       `json:"flow_1d_yi"`
	Flow5DYi         float64       `json:"flow_5d_yi"`
	Flow20DYi        float64       `json:"flow_20d_yi"`
	Flow60DYi        float64       `json:"flow_60d_yi"`
	VolRatio20       float64       `json:"vol_ratio_20"`
	VolumeSignal     string        `json:"volume_signal"`
	IsIntraday       bool          `json:"is_intraday"`
	VerifyStatus     string        `json:"verify_status"`
	SourceNote       string        `json:"source_note"`
	GovShares        float64       `json:"gov_shares"`         // 国家队合计法定份额（份）
	GovRatioPct      float64       `json:"gov_ratio_pct"`      // 国家队合计占比（%）
	GovMTMValueYi    float64       `json:"gov_mtm_value_yi"`   // 国家队最新重估市值（亿元 RMB）
	GovReportDate    string        `json:"gov_report_date"`
	GovReportType    string        `json:"gov_report_type"`
	GovHolders       []CNETFHolder `json:"gov_holders,omitempty"`
}

type US13FFiling struct {
	CIK             string  `json:"cik"`
	InstitutionName string  `json:"institution_name"`
	Category        string  `json:"category"`
	AccessionNumber string  `json:"accession_number"`
	ReportDate      string  `json:"report_date"`
	FilingDate      string  `json:"filing_date"`
	TotalValueUSD   float64 `json:"total_value_usd"` // 正股多头法定总市值（美元）
	PositionCount   int     `json:"position_count"`
	OptionCount     int     `json:"option_count"`
	SourceURL       string  `json:"source_url"`
	UpdatedAt       int64   `json:"updated_at"`
}

type US13FHolding struct {
	CIK             string  `json:"cik"`
	InstitutionName string  `json:"institution_name"`
	ReportDate      string  `json:"report_date"`
	CUSIP           string  `json:"cusip"`
	Ticker          string  `json:"ticker"`
	IssuerName      string  `json:"issuer_name"`
	AssetClass      string  `json:"asset_class"`     // EQUITY | CALL | PUT
	Shares          float64 `json:"shares"`
	PrevSharesAdj   float64 `json:"prev_shares_adj"`
	ShareDelta      float64 `json:"share_delta"`
	ChangePct       float64 `json:"change_pct"`
	Action          string  `json:"action"`          // NEW | ADD | REDUCE | SOLD_OUT | HOLD
	FilingValueUSD  float64 `json:"filing_value_usd"`// 13F 法定市值（美元）
	PortfolioWtPct  float64 `json:"portfolio_wt_pct"`
	QuarterVWAP     float64 `json:"quarter_vwap"`    // 季度均价（美元）
	EstFlowUSD      float64 `json:"est_flow_usd"`    // 估算净流向（美元）
	LatestPriceUSD  float64 `json:"latest_price_usd"`
	MTMValueUSD     float64 `json:"mtm_value_usd"`
	SourceURL       string  `json:"source_url"`
}

type USStockConsensus struct {
	Ticker            string  `json:"ticker"`
	CUSIP             string  `json:"cusip"`
	IssuerName        string  `json:"issuer_name"`
	ReportDate        string  `json:"report_date"`
	HolderCount       int     `json:"holder_count"`
	AddCount          int     `json:"add_count"`
	ReduceCount       int     `json:"reduce_count"`
	TotalFilingUSD    float64 `json:"total_filing_usd"`
	AggregateFlowUSD  float64 `json:"aggregate_flow_usd"`
	TotalShares       float64 `json:"total_shares"`
	NetShareDelta     float64 `json:"net_share_delta"`
}

type HKSouthboundDaily struct {
	Code            string  `json:"code"`
	Name            string  `json:"name"`
	SettlementDate  string  `json:"settlement_date"` // CCASS 交收日 D
	TradeDate       string  `json:"trade_date"`      // 对应交易日 T(D)
	SharesHeld      float64 `json:"shares_held"`
	ShareDelta      float64 `json:"share_delta"`
	HoldRatioPct    float64 `json:"hold_ratio_pct"`
	ClosePriceHKD   float64 `json:"close_price_hkd"`
	VWAPHKD         float64 `json:"vwap_hkd"`        // 全天真实成交均价 = Turnover / Volume
	TurnoverHKD     float64 `json:"turnover_hkd"`
	VolumeShares    float64 `json:"volume_shares"`
	HoldValueHKDYi  float64 `json:"hold_value_hkd_yi"`
	NetFlowHKDYi    float64 `json:"net_flow_hkd_yi"`
	Flow5DHKDYi     float64 `json:"flow_5d_hkd_yi"`
	Flow20DHKDYi    float64 `json:"flow_20d_hkd_yi"`
	SourceURL       string  `json:"source_url"`
	UpdatedAt       int64   `json:"updated_at"`
}

type HKCCASSSnapshot struct {
	Code               string  `json:"code"`
	SettlementDate     string  `json:"settlement_date"`
	TradeDate          string  `json:"trade_date"`
	TotalCCASSShares   float64 `json:"total_ccass_shares"`
	SouthboundShares   float64 `json:"southbound_shares"`
	ForeignShares      float64 `json:"foreign_shares"`
	CNInstShares       float64 `json:"cn_inst_shares"`
	InternetShares     float64 `json:"internet_shares"`    // 富途(B01955)/盈透(B01590)/辉立/耀才等
	SouthboundFlowYi   float64 `json:"southbound_flow_yi"`
	ForeignCleanFlowYi float64 `json:"foreign_clean_flow_yi"`
	ForeignRawFlowYi   float64 `json:"foreign_raw_flow_yi"`
	CNInstFlowYi       float64 `json:"cn_inst_flow_yi"`
	InternetFlowYi     float64 `json:"internet_flow_yi"`
	AnomalyFlag        string  `json:"anomaly_flag"`       // "" | CUSTODIAN_TRANSFER | ADR_CONVERSION
	AnomalyNote        string  `json:"anomaly_note"`
	SourceURL          string  `json:"source_url"`
	UpdatedAt          int64   `json:"updated_at"`
}

type HKBrokerPosition struct {
	Code            string  `json:"code"`
	SettlementDate  string  `json:"settlement_date"`
	ParticipantID   string  `json:"participant_id"` // 如 B01955 (富途), C00019 (港股通)
	ParticipantName string  `json:"participant_name"`
	Cohort          string  `json:"cohort"`         // southbound | foreign_inst | cn_inst | internet_retail | other
	Shares          float64 `json:"shares"`
	ShareDelta      float64 `json:"share_delta"`
	HoldRatioPct    float64 `json:"hold_ratio_pct"`
	FlowHKDYi       float64 `json:"flow_hkd_yi"`
	TransferFlag    string  `json:"transfer_flag"`
}

type CNFlowTrendPoint struct {
	TradeDate       string  `json:"trade_date"`
	DailyFlowYi     float64 `json:"daily_flow_yi"`
	CumulativeYi    float64 `json:"cumulative_yi"`
	CoreDailyFlowYi float64 `json:"core_daily_flow_yi"`
	CoreCumYi       float64 `json:"core_cumulative_yi"`
}

type SyncStatus struct {
	Market      string `json:"market"`
	Status      string `json:"status"` // ok | running | error
	LastSyncAt  int64  `json:"last_sync_at"`
	DataAsOf    string `json:"data_as_of"`
	ItemCount   int    `json:"item_count"`
	ErrorMsg    string `json:"error_msg"`
	SourceLabel string `json:"source_label"`
}
