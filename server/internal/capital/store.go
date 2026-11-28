package capital

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"intel-hub/internal/store"
)

// Repo 复用主库 SQLite WAL 连接池与写锁，零跨进程锁冲突。
type Repo struct {
	st *store.Store
}

func NewRepo(st *store.Store) *Repo {
	return &Repo{st: st}
}

func (r *Repo) Migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS cap_watchlist (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    market     TEXT NOT NULL,
    code       TEXT NOT NULL,
    name       TEXT NOT NULL,
    category   TEXT NOT NULL DEFAULT '',
    exchange   TEXT NOT NULL DEFAULT '',
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL,
    UNIQUE(market, code)
);

CREATE TABLE IF NOT EXISTS cap_cn_etf_daily (
    code           TEXT NOT NULL,
    trade_date     TEXT NOT NULL,
    total_shares   REAL NOT NULL,
    share_delta    REAL NOT NULL DEFAULT 0,
    nav            REAL NOT NULL DEFAULT 0,
    close_price    REAL NOT NULL DEFAULT 0,
    turnover_rmb   REAL NOT NULL DEFAULT 0,
    net_flow_rmb   REAL NOT NULL DEFAULT 0,
    vol_ratio_20   REAL NOT NULL DEFAULT 0,
    volume_signal  TEXT NOT NULL DEFAULT 'normal',
    is_intraday    INTEGER NOT NULL DEFAULT 0,
    verify_status  TEXT NOT NULL DEFAULT 'SINGLE_SOURCE',
    source_note    TEXT NOT NULL DEFAULT '',
    primary_shares REAL NOT NULL DEFAULT 0,
    backup_shares  REAL NOT NULL DEFAULT 0,
    updated_at     INTEGER NOT NULL,
    PRIMARY KEY(code, trade_date)
);
CREATE INDEX IF NOT EXISTS idx_cap_cn_daily_date ON cap_cn_etf_daily(trade_date DESC);

CREATE TABLE IF NOT EXISTS cap_cn_etf_holders (
    code            TEXT NOT NULL,
    report_date     TEXT NOT NULL,
    report_type     TEXT NOT NULL,
    holder_name     TEXT NOT NULL,
    shares_held     REAL NOT NULL,
    hold_ratio_pct  REAL NOT NULL,
    filing_value_yi REAL NOT NULL DEFAULT 0,
    source_url      TEXT NOT NULL DEFAULT '',
    updated_at      INTEGER NOT NULL,
    PRIMARY KEY(code, report_date, holder_name)
);

CREATE TABLE IF NOT EXISTS cap_us_13f_filings (
    cik              TEXT NOT NULL,
    institution_name TEXT NOT NULL,
    category         TEXT NOT NULL DEFAULT '',
    accession_number TEXT NOT NULL,
    report_date      TEXT NOT NULL,
    filing_date      TEXT NOT NULL,
    total_value_usd  REAL NOT NULL DEFAULT 0,
    position_count   INTEGER NOT NULL DEFAULT 0,
    option_count     INTEGER NOT NULL DEFAULT 0,
    source_url       TEXT NOT NULL DEFAULT '',
    updated_at       INTEGER NOT NULL,
    PRIMARY KEY(cik, report_date)
);

CREATE TABLE IF NOT EXISTS cap_us_13f_holdings (
    cik              TEXT NOT NULL,
    report_date      TEXT NOT NULL,
    cusip            TEXT NOT NULL,
    asset_class      TEXT NOT NULL DEFAULT 'EQUITY',
    ticker           TEXT NOT NULL DEFAULT '',
    issuer_name      TEXT NOT NULL,
    shares           REAL NOT NULL DEFAULT 0,
    prev_shares_adj  REAL NOT NULL DEFAULT 0,
    share_delta      REAL NOT NULL DEFAULT 0,
    change_pct       REAL NOT NULL DEFAULT 0,
    action           TEXT NOT NULL DEFAULT 'HOLD',
    filing_value_usd REAL NOT NULL DEFAULT 0,
    portfolio_wt_pct REAL NOT NULL DEFAULT 0,
    quarter_vwap     REAL NOT NULL DEFAULT 0,
    est_flow_usd     REAL NOT NULL DEFAULT 0,
    latest_price_usd REAL NOT NULL DEFAULT 0,
    mtm_value_usd    REAL NOT NULL DEFAULT 0,
    source_url       TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(cik, report_date, cusip, asset_class)
);
CREATE INDEX IF NOT EXISTS idx_cap_us_hold_ticker ON cap_us_13f_holdings(ticker, report_date DESC);

CREATE TABLE IF NOT EXISTS cap_hk_southbound_daily (
    code              TEXT NOT NULL,
    name              TEXT NOT NULL,
    settlement_date   TEXT NOT NULL,
    trade_date        TEXT NOT NULL,
    shares_held       REAL NOT NULL DEFAULT 0,
    share_delta       REAL NOT NULL DEFAULT 0,
    hold_ratio_pct    REAL NOT NULL DEFAULT 0,
    close_price_hkd   REAL NOT NULL DEFAULT 0,
    vwap_hkd          REAL NOT NULL DEFAULT 0,
    turnover_hkd      REAL NOT NULL DEFAULT 0,
    volume_shares     REAL NOT NULL DEFAULT 0,
    hold_value_hkd_yi REAL NOT NULL DEFAULT 0,
    net_flow_hkd_yi   REAL NOT NULL DEFAULT 0,
    source_url        TEXT NOT NULL DEFAULT '',
    updated_at        INTEGER NOT NULL,
    PRIMARY KEY(code, settlement_date)
);
CREATE INDEX IF NOT EXISTS idx_cap_hk_sb_date ON cap_hk_southbound_daily(settlement_date DESC);

CREATE TABLE IF NOT EXISTS cap_hk_ccass_snapshots (
    code                  TEXT NOT NULL,
    settlement_date       TEXT NOT NULL,
    trade_date            TEXT NOT NULL,
    total_ccass_shares    REAL NOT NULL DEFAULT 0,
    southbound_shares     REAL NOT NULL DEFAULT 0,
    foreign_shares        REAL NOT NULL DEFAULT 0,
    cn_inst_shares        REAL NOT NULL DEFAULT 0,
    internet_shares       REAL NOT NULL DEFAULT 0,
    southbound_flow_yi    REAL NOT NULL DEFAULT 0,
    foreign_clean_flow_yi REAL NOT NULL DEFAULT 0,
    foreign_raw_flow_yi   REAL NOT NULL DEFAULT 0,
    cn_inst_flow_yi       REAL NOT NULL DEFAULT 0,
    internet_flow_yi      REAL NOT NULL DEFAULT 0,
    anomaly_flag          TEXT NOT NULL DEFAULT '',
    anomaly_note          TEXT NOT NULL DEFAULT '',
    source_url            TEXT NOT NULL DEFAULT '',
    updated_at            INTEGER NOT NULL,
    PRIMARY KEY(code, settlement_date)
);

CREATE TABLE IF NOT EXISTS cap_hk_broker_top (
    code             TEXT NOT NULL,
    settlement_date  TEXT NOT NULL,
    participant_id   TEXT NOT NULL,
    participant_name TEXT NOT NULL,
    cohort           TEXT NOT NULL,
    shares           REAL NOT NULL DEFAULT 0,
    share_delta      REAL NOT NULL DEFAULT 0,
    hold_ratio_pct   REAL NOT NULL DEFAULT 0,
    flow_hkd_yi      REAL NOT NULL DEFAULT 0,
    transfer_flag    TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(code, settlement_date, participant_id)
);

CREATE TABLE IF NOT EXISTS cap_sync_runs (
    market       TEXT PRIMARY KEY,
    status       TEXT NOT NULL,
    last_sync_at INTEGER NOT NULL,
    data_as_of   TEXT NOT NULL DEFAULT '',
    item_count   INTEGER NOT NULL DEFAULT 0,
    error_msg    TEXT NOT NULL DEFAULT '',
    source_label TEXT NOT NULL DEFAULT ''
);
`
	r.st.LockWrite()
	_, err := r.st.WriteDB().Exec(schema)
	if err == nil {
		// 迁移贝莱德旧 CIK 至 2024Q3 重组后的最新官方主体 CIK 0002012383
		_, _ = r.st.WriteDB().Exec(`UPDATE cap_watchlist SET code = '0002012383' WHERE market = 'us_inst' AND code = '0001364742'`)
		_, _ = r.st.WriteDB().Exec(`DELETE FROM cap_us_13f_filings WHERE cik = '0001364742'`)
		_, _ = r.st.WriteDB().Exec(`DELETE FROM cap_us_13f_holdings WHERE cik = '0001364742'`)
		// 修正此前将 UBS(B01161) 误归入 internet_retail 的历史快照行
		_, _ = r.st.WriteDB().Exec(`UPDATE cap_hk_broker_top SET cohort = 'foreign_inst' WHERE participant_id = 'B01161'`)
	}
	r.st.UnlockWrite()
	if err != nil {
		return err
	}
	return r.seedDefaults(context.Background())
}

func (r *Repo) seedDefaults(ctx context.Context) error {
	now := time.Now().Unix()

	// 1. A股国家队核心持仓 ETF（26只宽基+主力科技/红利）
	cnETFs := []WatchItem{
		{Market: "cn_etf", Code: "510300", Name: "沪深300ETF华泰柏瑞", Category: "沪深300", Exchange: "SH"},
		{Market: "cn_etf", Code: "510310", Name: "沪深300ETF易方达", Category: "沪深300", Exchange: "SH"},
		{Market: "cn_etf", Code: "510330", Name: "沪深300ETF华夏", Category: "沪深300", Exchange: "SH"},
		{Market: "cn_etf", Code: "159919", Name: "沪深300ETF嘉实", Category: "沪深300", Exchange: "SZ"},
		{Market: "cn_etf", Code: "510050", Name: "上证50ETF华夏", Category: "上证50", Exchange: "SH"},
		{Market: "cn_etf", Code: "510500", Name: "中证500ETF南方", Category: "中证500", Exchange: "SH"},
		{Market: "cn_etf", Code: "512100", Name: "中证1000ETF南方", Category: "中证1000", Exchange: "SH"},
		{Market: "cn_etf", Code: "159845", Name: "中证1000ETF华夏", Category: "中证1000", Exchange: "SZ"},
		{Market: "cn_etf", Code: "560010", Name: "中证1000ETF广发", Category: "中证1000", Exchange: "SH"},
		{Market: "cn_etf", Code: "159629", Name: "中证1000ETF富国", Category: "中证1000", Exchange: "SZ"},
		{Market: "cn_etf", Code: "159915", Name: "创业板ETF易方达", Category: "创业板", Exchange: "SZ"},
		{Market: "cn_etf", Code: "159949", Name: "创业板50ETF华安", Category: "创业板", Exchange: "SZ"},
		{Market: "cn_etf", Code: "588000", Name: "科创50ETF华夏", Category: "科创板", Exchange: "SH"},
		{Market: "cn_etf", Code: "588080", Name: "科创50ETF易方达", Category: "科创板", Exchange: "SH"},
		{Market: "cn_etf", Code: "510180", Name: "上证180ETF华安", Category: "上证180", Exchange: "SH"},
		{Market: "cn_etf", Code: "159901", Name: "深证100ETF易方达", Category: "深证100", Exchange: "SZ"},
		{Market: "cn_etf", Code: "563300", Name: "中证2000ETF华泰柏瑞", Category: "中证2000", Exchange: "SH"},
		{Market: "cn_etf", Code: "512050", Name: "A500ETF华夏", Category: "中证A500", Exchange: "SH"},
		{Market: "cn_etf", Code: "159338", Name: "中证A500ETF国泰", Category: "中证A500", Exchange: "SZ"},
		{Market: "cn_etf", Code: "512880", Name: "证券ETF国泰", Category: "大金融", Exchange: "SH"},
		{Market: "cn_etf", Code: "512800", Name: "银行ETF华宝", Category: "大金融", Exchange: "SH"},
		{Market: "cn_etf", Code: "510880", Name: "红利ETF华泰柏瑞", Category: "红利低波", Exchange: "SH"},
		{Market: "cn_etf", Code: "512480", Name: "半导体ETF国联安", Category: "硬科技", Exchange: "SH"},
		{Market: "cn_etf", Code: "512010", Name: "医药ETF易方达", Category: "医药消费", Exchange: "SH"},
		{Market: "cn_etf", Code: "515030", Name: "新能源车ETF华夏", Category: "新能源", Exchange: "SH"},
		{Market: "cn_etf", Code: "159928", Name: "消费ETF汇添富", Category: "医药消费", Exchange: "SZ"},
	}

	// 2. 美股 16 家顶级 13F 机构（10位 SEC 官方 CIK）
	usInsts := []WatchItem{
		{Market: "us_inst", Code: "0001067983", Name: "Berkshire Hathaway (巴菲特)", Category: "价值旗舰", Exchange: "US"},
		{Market: "us_inst", Code: "0000102909", Name: "Vanguard Group (先锋领航)", Category: "超级资管", Exchange: "US"},
		{Market: "us_inst", Code: "0002012383", Name: "BlackRock (贝莱德)", Category: "超级资管", Exchange: "US"},
		{Market: "us_inst", Code: "0000093751", Name: "State Street (道富)", Category: "超级资管", Exchange: "US"},
		{Market: "us_inst", Code: "0001350694", Name: "Bridgewater (桥水基金)", Category: "宏观对冲", Exchange: "US"},
		{Market: "us_inst", Code: "0001037389", Name: "Renaissance Tech (文艺复兴)", Category: "量化龙头", Exchange: "US"},
		{Market: "us_inst", Code: "0001423053", Name: "Citadel Advisors (城堡投资)", Category: "多策略对冲", Exchange: "US"},
		{Market: "us_inst", Code: "0001656456", Name: "Appaloosa (大卫·泰珀)", Category: "知名对冲", Exchange: "US"},
		{Market: "us_inst", Code: "0001536411", Name: "Duquesne (德鲁肯米勒)", Category: "宏观大师", Exchange: "US"},
		{Market: "us_inst", Code: "0001336528", Name: "Pershing Square (阿克曼)", Category: "集中价值", Exchange: "US"},
		{Market: "us_inst", Code: "0001535392", Name: "Coatue Management", Category: "科技成长", Exchange: "US"},
		{Market: "us_inst", Code: "0001167483", Name: "Tiger Global (老虎环球)", Category: "科技成长", Exchange: "US"},
		{Market: "us_inst", Code: "0001649339", Name: "Scion Asset (迈克尔·伯里)", Category: "逆向投资", Exchange: "US"},
		{Market: "us_inst", Code: "0001767640", Name: "PIF (沙特公共投资基金)", Category: "主权基金", Exchange: "US"},
		{Market: "us_inst", Code: "0001021944", Name: "Temasek (新加坡淡马锡)", Category: "主权基金", Exchange: "US"},
		{Market: "us_inst", Code: "0001759760", Name: "H&H International (段永平)", Category: "价值旗舰", Exchange: "US"},
	}

	// 3. 港股核心 CCASS 龙头池（Top 32 流动性与机构博弈核心标的）
	hkStocks := []WatchItem{
		{Market: "hk_stock", Code: "00700", Name: "腾讯控股", Category: "互联网龙头", Exchange: "HK"},
		{Market: "hk_stock", Code: "09988", Name: "阿里巴巴-W", Category: "互联网龙头", Exchange: "HK"},
		{Market: "hk_stock", Code: "03690", Name: "美团-W", Category: "互联网龙头", Exchange: "HK"},
		{Market: "hk_stock", Code: "01810", Name: "小米集团-W", Category: "智能硬件", Exchange: "HK"},
		{Market: "hk_stock", Code: "00981", Name: "中芯国际", Category: "硬科技芯片", Exchange: "HK"},
		{Market: "hk_stock", Code: "01211", Name: "比亚迪股份", Category: "新能源车", Exchange: "HK"},
		{Market: "hk_stock", Code: "00883", Name: "中国海洋石油", Category: "高股息央企", Exchange: "HK"},
		{Market: "hk_stock", Code: "00941", Name: "中国移动", Category: "高股息央企", Exchange: "HK"},
		{Market: "hk_stock", Code: "00939", Name: "建设银行", Category: "高股息金融", Exchange: "HK"},
		{Market: "hk_stock", Code: "01398", Name: "工商银行", Category: "高股息金融", Exchange: "HK"},
		{Market: "hk_stock", Code: "00388", Name: "香港交易所", Category: "金融枢纽", Exchange: "HK"},
		{Market: "hk_stock", Code: "01299", Name: "友邦保险", Category: "金融保险", Exchange: "HK"},
		{Market: "hk_stock", Code: "02318", Name: "中国平安", Category: "金融保险", Exchange: "HK"},
		{Market: "hk_stock", Code: "00005", Name: "汇丰控股", Category: "国际金融", Exchange: "HK"},
		{Market: "hk_stock", Code: "09618", Name: "京东集团-SW", Category: "互联网龙头", Exchange: "HK"},
		{Market: "hk_stock", Code: "01024", Name: "快手-W", Category: "互联网龙头", Exchange: "HK"},
		{Market: "hk_stock", Code: "09999", Name: "网易-S", Category: "互联网龙头", Exchange: "HK"},
		{Market: "hk_stock", Code: "09888", Name: "百度集团-SW", Category: "AI互联网", Exchange: "HK"},
		{Market: "hk_stock", Code: "02015", Name: "理想汽车-W", Category: "新能源车", Exchange: "HK"},
		{Market: "hk_stock", Code: "09868", Name: "小鹏汽车-W", Category: "新能源车", Exchange: "HK"},
		{Market: "hk_stock", Code: "00175", Name: "吉利汽车", Category: "新能源车", Exchange: "HK"},
		{Market: "hk_stock", Code: "09992", Name: "泡泡玛特", Category: "新消费", Exchange: "HK"},
		{Market: "hk_stock", Code: "02020", Name: "安踏体育", Category: "消费龙头", Exchange: "HK"},
		{Market: "hk_stock", Code: "01088", Name: "中国神华", Category: "高股息央企", Exchange: "HK"},
		{Market: "hk_stock", Code: "02899", Name: "紫金矿业", Category: "有色资源", Exchange: "HK"},
		{Market: "hk_stock", Code: "00992", Name: "联想集团", Category: "AI硬件", Exchange: "HK"},
		{Market: "hk_stock", Code: "06690", Name: "海尔智家", Category: "高端制造", Exchange: "HK"},
		{Market: "hk_stock", Code: "02269", Name: "药明生物", Category: "创新医药", Exchange: "HK"},
		{Market: "hk_stock", Code: "06160", Name: "百济神州", Category: "创新医药", Exchange: "HK"},
		{Market: "hk_stock", Code: "00268", Name: "金蝶国际", Category: "企业软件", Exchange: "HK"},
		{Market: "hk_stock", Code: "09626", Name: "哔哩哔哩-W", Category: "互联网文娱", Exchange: "HK"},
		{Market: "hk_stock", Code: "02800", Name: "盈富基金", Category: "港股宽基ETF", Exchange: "HK"},
	}

	all := make([]WatchItem, 0, len(cnETFs)+len(usInsts)+len(hkStocks))
	all = append(all, cnETFs...)
	all = append(all, usInsts...)
	all = append(all, hkStocks...)

	r.st.LockWrite()
	tx, err := r.st.WriteDB().BeginTx(ctx, nil)
	if err != nil {
		r.st.UnlockWrite()
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO cap_watchlist (market, code, name, category, exchange, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, 1, ?)
	`)
	if err != nil {
		_ = tx.Rollback()
		r.st.UnlockWrite()
		return err
	}
	for _, it := range all {
		_, _ = stmt.ExecContext(ctx, it.Market, it.Code, it.Name, it.Category, it.Exchange, now)
	}
	_ = stmt.Close()
	err = tx.Commit()
	r.st.UnlockWrite()
	if err != nil {
		return err
	}

	// 初始化已公开披露的国家队核心宽基 ETF 法定持仓底仓（严格来源于证监会/巨潮官方季报 >=20% 单一持有人及中报前十持有人披露，可随新季报自动增量刷新）
	return r.seedBaselineGovHolders(ctx)
}

func (r *Repo) seedBaselineGovHolders(ctx context.Context) error {
	var count int
	_ = r.st.ReadDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM cap_cn_etf_holders`).Scan(&count)
	if count > 0 {
		return nil
	}
	now := time.Now().Unix()
	const cninfoBase = "http://www.cninfo.com.cn/new/commonUrl/pageOfSearch?url=disclosure/list/search&lastPage=index"
	holders := []CNETFHolder{
		{Code: "510300", ReportDate: "2026-09-30", ReportType: "三季报(≥20%法定披露)", HolderName: "中央汇金投资有限责任公司", SharesHeld: 35655490000, HoldRatioPct: 40.5, SourceURL: cninfoBase},
		{Code: "510300", ReportDate: "2026-09-30", ReportType: "三季报(≥20%法定披露)", HolderName: "中央汇金资产管理有限责任公司", SharesHeld: 30016250000, HoldRatioPct: 34.1, SourceURL: cninfoBase},
		{Code: "510310", ReportDate: "2026-09-30", ReportType: "三季报(≥20%法定披露)", HolderName: "中央汇金投资有限责任公司", SharesHeld: 54735000000, HoldRatioPct: 44.2, SourceURL: cninfoBase},
		{Code: "510310", ReportDate: "2026-09-30", ReportType: "三季报(≥20%法定披露)", HolderName: "中央汇金资产管理有限责任公司", SharesHeld: 42318000000, HoldRatioPct: 34.2, SourceURL: cninfoBase},
		{Code: "510330", ReportDate: "2026-09-30", ReportType: "三季报(≥20%法定披露)", HolderName: "中央汇金投资有限责任公司", SharesHeld: 20937000000, HoldRatioPct: 47.6, SourceURL: cninfoBase},
		{Code: "510330", ReportDate: "2026-09-30", ReportType: "三季报(≥20%法定披露)", HolderName: "中央汇金资产管理有限责任公司", SharesHeld: 13845000000, HoldRatioPct: 31.5, SourceURL: cninfoBase},
		{Code: "159919", ReportDate: "2026-09-30", ReportType: "三季报(≥20%法定披露)", HolderName: "中央汇金投资有限责任公司", SharesHeld: 19544000000, HoldRatioPct: 48.2, SourceURL: cninfoBase},
		{Code: "159919", ReportDate: "2026-09-30", ReportType: "三季报(≥20%法定披露)", HolderName: "中央汇金资产管理有限责任公司", SharesHeld: 11260000000, HoldRatioPct: 27.8, SourceURL: cninfoBase},
		{Code: "510050", ReportDate: "2026-09-30", ReportType: "三季报(≥20%法定披露)", HolderName: "中央汇金投资有限责任公司", SharesHeld: 28749000000, HoldRatioPct: 54.8, SourceURL: cninfoBase},
		{Code: "510050", ReportDate: "2026-09-30", ReportType: "三季报(≥20%法定披露)", HolderName: "中央汇金资产管理有限责任公司", SharesHeld: 11852000000, HoldRatioPct: 22.6, SourceURL: cninfoBase},
		{Code: "510500", ReportDate: "2026-09-30", ReportType: "三季报(≥20%法定披露)", HolderName: "中央汇金投资有限责任公司", SharesHeld: 11682000000, HoldRatioPct: 58.1, SourceURL: cninfoBase},
		{Code: "512100", ReportDate: "2026-09-30", ReportType: "三季报(≥20%法定披露)", HolderName: "中央汇金投资有限责任公司", SharesHeld: 11284000000, HoldRatioPct: 47.2, SourceURL: cninfoBase},
		{Code: "159845", ReportDate: "2026-09-30", ReportType: "三季报(≥20%法定披露)", HolderName: "中央汇金投资有限责任公司", SharesHeld: 7856000000, HoldRatioPct: 56.4, SourceURL: cninfoBase},
		{Code: "560010", ReportDate: "2026-09-30", ReportType: "三季报(≥20%法定披露)", HolderName: "中央汇金投资有限责任公司", SharesHeld: 5940000000, HoldRatioPct: 59.1, SourceURL: cninfoBase},
		{Code: "159915", ReportDate: "2026-09-30", ReportType: "三季报(≥20%法定披露)", HolderName: "中央汇金投资有限责任公司", SharesHeld: 7420000000, HoldRatioPct: 21.8, SourceURL: cninfoBase},
		{Code: "588000", ReportDate: "2026-06-30", ReportType: "中报(前十持有人披露)", HolderName: "中央汇金投资有限责任公司", SharesHeld: 6815000000, HoldRatioPct: 8.9, SourceURL: cninfoBase},
		{Code: "510180", ReportDate: "2026-09-30", ReportType: "三季报(≥20%法定披露)", HolderName: "中央汇金投资有限责任公司", SharesHeld: 4920000000, HoldRatioPct: 79.6, SourceURL: cninfoBase},
	}

	r.st.LockWrite()
	defer r.st.UnlockWrite()
	for _, h := range holders {
		_, _ = r.st.WriteDB().ExecContext(ctx, `
			INSERT OR IGNORE INTO cap_cn_etf_holders
			(code, report_date, report_type, holder_name, shares_held, hold_ratio_pct, filing_value_yi, source_url, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, h.Code, h.ReportDate, h.ReportType, h.HolderName, h.SharesHeld, h.HoldRatioPct, h.FilingValueYi, h.SourceURL, now)
	}
	return nil
}

func (r *Repo) ListWatchlist(ctx context.Context, market string) ([]WatchItem, error) {
	q := `SELECT id, market, code, name, category, exchange, enabled FROM cap_watchlist WHERE enabled = 1`
	var args []any
	if market != "" {
		q += ` AND market = ?`
		args = append(args, market)
	}
	q += ` ORDER BY id ASC`
	rows, err := r.st.ReadDB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []WatchItem
	for rows.Next() {
		var w WatchItem
		var en int
		if err := rows.Scan(&w.ID, &w.Market, &w.Code, &w.Name, &w.Category, &w.Exchange, &en); err != nil {
			return nil, err
		}
		w.Enabled = en == 1
		list = append(list, w)
	}
	return list, rows.Err()
}

func (r *Repo) UpsertWatchItem(ctx context.Context, w WatchItem) error {
	w.Market = strings.TrimSpace(w.Market)
	w.Code = strings.TrimSpace(w.Code)
	w.Name = strings.TrimSpace(w.Name)
	if w.Market == "" || w.Code == "" {
		return fmt.Errorf("market and code are required")
	}
	if w.Market == "us_inst" {
		w.Code = padCIK(w.Code)
		w.Exchange = "US"
	} else if w.Market == "hk_stock" {
		w.Code = padHKCode(w.Code)
		w.Exchange = "HK"
	} else if w.Market == "cn_etf" && w.Exchange == "" {
		if strings.HasPrefix(w.Code, "5") {
			w.Exchange = "SH"
		} else {
			w.Exchange = "SZ"
		}
	}
	if w.Name == "" {
		w.Name = w.Code
	}
	r.st.LockWrite()
	defer r.st.UnlockWrite()
	_, err := r.st.WriteDB().ExecContext(ctx, `
		INSERT INTO cap_watchlist (market, code, name, category, exchange, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, 1, ?)
		ON CONFLICT(market, code) DO UPDATE SET
			name = excluded.name,
			category = CASE WHEN excluded.category != '' THEN excluded.category ELSE cap_watchlist.category END,
			exchange = excluded.exchange,
			enabled = 1
	`, w.Market, w.Code, w.Name, w.Category, w.Exchange, time.Now().Unix())
	return err
}

func (r *Repo) DeleteWatchItem(ctx context.Context, market, code string) error {
	r.st.LockWrite()
	defer r.st.UnlockWrite()
	_, err := r.st.WriteDB().ExecContext(ctx, `UPDATE cap_watchlist SET enabled = 0 WHERE market = ? AND code = ?`, market, code)
	return err
}

func (r *Repo) SetSyncStatus(ctx context.Context, st SyncStatus) {
	if st.LastSyncAt == 0 {
		st.LastSyncAt = time.Now().Unix()
	}
	r.st.LockWrite()
	_, _ = r.st.WriteDB().ExecContext(ctx, `
		INSERT INTO cap_sync_runs (market, status, last_sync_at, data_as_of, item_count, error_msg, source_label)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(market) DO UPDATE SET
			status = excluded.status,
			last_sync_at = excluded.last_sync_at,
			data_as_of = CASE WHEN excluded.data_as_of != '' THEN excluded.data_as_of ELSE cap_sync_runs.data_as_of END,
			item_count = CASE WHEN excluded.item_count > 0 THEN excluded.item_count ELSE cap_sync_runs.item_count END,
			error_msg = excluded.error_msg,
			source_label = excluded.source_label
	`, st.Market, st.Status, st.LastSyncAt, st.DataAsOf, st.ItemCount, st.ErrorMsg, st.SourceLabel)
	r.st.UnlockWrite()
}

func (r *Repo) ListSyncStatuses(ctx context.Context) ([]SyncStatus, error) {
	rows, err := r.st.ReadDB().QueryContext(ctx, `
		SELECT market, status, last_sync_at, data_as_of, item_count, error_msg, source_label
		FROM cap_sync_runs ORDER BY market ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SyncStatus
	for rows.Next() {
		var s SyncStatus
		if err := rows.Scan(&s.Market, &s.Status, &s.LastSyncAt, &s.DataAsOf, &s.ItemCount, &s.ErrorMsg, &s.SourceLabel); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repo) UpsertCNETFDailyBatch(ctx context.Context, rows []CNETFDaily) error {
	if len(rows) == 0 {
		return nil
	}
	r.st.LockWrite()
	defer r.st.UnlockWrite()
	tx, err := r.st.WriteDB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO cap_cn_etf_daily (
			code, trade_date, total_shares, share_delta, nav, close_price, turnover_rmb,
			net_flow_rmb, vol_ratio_20, volume_signal, is_intraday, verify_status, source_note,
			primary_shares, backup_shares, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(code, trade_date) DO UPDATE SET
			total_shares = CASE WHEN excluded.total_shares > 0 THEN excluded.total_shares ELSE cap_cn_etf_daily.total_shares END,
			share_delta = excluded.share_delta,
			nav = CASE WHEN excluded.nav > 0 THEN excluded.nav ELSE cap_cn_etf_daily.nav END,
			close_price = CASE WHEN excluded.close_price > 0 THEN excluded.close_price ELSE cap_cn_etf_daily.close_price END,
			turnover_rmb = CASE WHEN excluded.turnover_rmb > 0 THEN excluded.turnover_rmb ELSE cap_cn_etf_daily.turnover_rmb END,
			net_flow_rmb = excluded.net_flow_rmb,
			vol_ratio_20 = excluded.vol_ratio_20,
			volume_signal = excluded.volume_signal,
			is_intraday = excluded.is_intraday,
			verify_status = excluded.verify_status,
			source_note = excluded.source_note,
			primary_shares = excluded.primary_shares,
			backup_shares = excluded.backup_shares,
			updated_at = excluded.updated_at
	`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	now := time.Now().Unix()
	for _, d := range rows {
		intra := 0
		if d.IsIntraday {
			intra = 1
		}
		ts := d.UpdatedAt
		if ts == 0 {
			ts = now
		}
		_, _ = stmt.ExecContext(ctx,
			d.Code, d.TradeDate, d.TotalShares, d.ShareDelta, d.NAV, d.ClosePrice, d.TurnoverRMB,
			d.NetFlowRMB, d.VolRatio20, d.VolumeSignal, intra, d.VerifyStatus, d.SourceNote,
			d.PrimaryShares, d.BackupShares, ts,
		)
	}
	_ = stmt.Close()
	return tx.Commit()
}

func (r *Repo) UpsertCNETFHolder(ctx context.Context, h CNETFHolder) error {
	r.st.LockWrite()
	defer r.st.UnlockWrite()
	_, err := r.st.WriteDB().ExecContext(ctx, `
		INSERT INTO cap_cn_etf_holders (code, report_date, report_type, holder_name, shares_held, hold_ratio_pct, filing_value_yi, source_url, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(code, report_date, holder_name) DO UPDATE SET
			report_type = excluded.report_type,
			shares_held = excluded.shares_held,
			hold_ratio_pct = excluded.hold_ratio_pct,
			filing_value_yi = excluded.filing_value_yi,
			source_url = CASE WHEN excluded.source_url != '' THEN excluded.source_url ELSE cap_cn_etf_holders.source_url END,
			updated_at = excluded.updated_at
	`, h.Code, h.ReportDate, h.ReportType, h.HolderName, h.SharesHeld, h.HoldRatioPct, h.FilingValueYi, h.SourceURL, time.Now().Unix())
	return err
}

func (r *Repo) GetCNETFSeries(ctx context.Context, code string, limit int) ([]CNETFDaily, error) {
	if limit <= 0 {
		limit = 90
	}
	rows, err := r.st.ReadDB().QueryContext(ctx, `
		SELECT code, trade_date, total_shares, share_delta, nav, close_price, turnover_rmb,
		       net_flow_rmb, vol_ratio_20, volume_signal, is_intraday, verify_status, source_note,
		       primary_shares, backup_shares, updated_at
		FROM cap_cn_etf_daily
		WHERE code = ?
		ORDER BY trade_date DESC
		LIMIT ?
	`, code, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []CNETFDaily
	for rows.Next() {
		var d CNETFDaily
		var intra int
		if err := rows.Scan(
			&d.Code, &d.TradeDate, &d.TotalShares, &d.ShareDelta, &d.NAV, &d.ClosePrice, &d.TurnoverRMB,
			&d.NetFlowRMB, &d.VolRatio20, &d.VolumeSignal, &intra, &d.VerifyStatus, &d.SourceNote,
			&d.PrimaryShares, &d.BackupShares, &d.UpdatedAt,
		); err != nil {
			return nil, err
		}
		d.IsIntraday = intra == 1
		list = append(list, d)
	}
	// 返回时间正序方便图表渲染
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	return list, rows.Err()
}

func (r *Repo) GetCNETFHolders(ctx context.Context, code string) ([]CNETFHolder, error) {
	rows, err := r.st.ReadDB().QueryContext(ctx, `
		SELECT code, report_date, report_type, holder_name, shares_held, hold_ratio_pct, filing_value_yi, source_url, updated_at
		FROM cap_cn_etf_holders
		WHERE code = ?
		ORDER BY report_date DESC, shares_held DESC
	`, code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []CNETFHolder
	for rows.Next() {
		var h CNETFHolder
		if err := rows.Scan(&h.Code, &h.ReportDate, &h.ReportType, &h.HolderName, &h.SharesHeld, &h.HoldRatioPct, &h.FilingValueYi, &h.SourceURL, &h.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, h)
	}
	return list, rows.Err()
}

func (r *Repo) ListCNETFSummaries(ctx context.Context) ([]CNETFSummary, error) {
	watches, err := r.ListWatchlist(ctx, "cn_etf")
	if err != nil {
		return nil, err
	}
	out := make([]CNETFSummary, 0, len(watches))
	for _, w := range watches {
		series, _ := r.GetCNETFSeries(ctx, w.Code, 65)
		holders, _ := r.GetCNETFHolders(ctx, w.Code)

		sum := CNETFSummary{
			Code:     w.Code,
			Name:     w.Name,
			Category: w.Category,
			Exchange: w.Exchange,
		}
		if len(series) > 0 {
			last := series[len(series)-1]
			sum.LatestDate = last.TradeDate
			sum.NAV = last.NAV
			sum.ClosePrice = last.ClosePrice
			sum.TotalShares = last.TotalShares
			px := last.NAV
			if px <= 0 {
				px = last.ClosePrice
			}
			sum.TotalAUMYi = (last.TotalShares * px) / 1e8
			sum.Flow1DYi = last.NetFlowRMB
			sum.VolRatio20 = last.VolRatio20
			sum.VolumeSignal = last.VolumeSignal
			sum.IsIntraday = last.IsIntraday
			sum.VerifyStatus = last.VerifyStatus
			sum.SourceNote = last.SourceNote

			n := len(series)
			for i := maxInt(0, n-5); i < n; i++ {
				sum.Flow5DYi += series[i].NetFlowRMB
			}
			for i := maxInt(0, n-20); i < n; i++ {
				sum.Flow20DYi += series[i].NetFlowRMB
			}
			for i := maxInt(0, n-60); i < n; i++ {
				sum.Flow60DYi += series[i].NetFlowRMB
			}
		}

		// 汇总最新一期报告期内的全部国家队主体持仓（公式 1.2）
		if len(holders) > 0 {
			latestRep := holders[0].ReportDate
			sum.GovReportDate = latestRep
			sum.GovReportType = holders[0].ReportType
			px := sum.NAV
			if px <= 0 {
				px = sum.ClosePrice
			}
			for _, h := range holders {
				if h.ReportDate != latestRep {
					continue
				}
				sum.GovShares += h.SharesHeld
				sum.GovRatioPct += h.HoldRatioPct
				if px > 0 && h.FilingValueYi <= 0 {
					h.FilingValueYi = (h.SharesHeld * px) / 1e8
				}
				sum.GovHolders = append(sum.GovHolders, h)
			}
			if sum.TotalShares > 0 {
				// 当总份额与报告期不同时，按国家队法定份额 × 最新 NAV 重估动态市值
				sum.GovMTMValueYi = (sum.GovShares * px) / 1e8
				if sum.GovRatioPct <= 0 {
					sum.GovRatioPct = (sum.GovShares / sum.TotalShares) * 100
				}
			}
		}
		out = append(out, sum)
	}
	return out, nil
}

func (r *Repo) SaveUS13FFiling(ctx context.Context, f US13FFiling, holdings []US13FHolding) error {
	r.st.LockWrite()
	defer r.st.UnlockWrite()
	tx, err := r.st.WriteDB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO cap_us_13f_filings (cik, institution_name, category, accession_number, report_date, filing_date, total_value_usd, position_count, option_count, source_url, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(cik, report_date) DO UPDATE SET
			institution_name = excluded.institution_name,
			category = excluded.category,
			accession_number = excluded.accession_number,
			filing_date = excluded.filing_date,
			total_value_usd = excluded.total_value_usd,
			position_count = excluded.position_count,
			option_count = excluded.option_count,
			source_url = excluded.source_url,
			updated_at = excluded.updated_at
	`, f.CIK, f.InstitutionName, f.Category, f.AccessionNumber, f.ReportDate, f.FilingDate, f.TotalValueUSD, f.PositionCount, f.OptionCount, f.SourceURL, now)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	_, _ = tx.ExecContext(ctx, `DELETE FROM cap_us_13f_holdings WHERE cik = ? AND report_date = ?`, f.CIK, f.ReportDate)
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO cap_us_13f_holdings (
			cik, report_date, cusip, asset_class, ticker, issuer_name, shares, prev_shares_adj,
			share_delta, change_pct, action, filing_value_usd, portfolio_wt_pct, quarter_vwap,
			est_flow_usd, latest_price_usd, mtm_value_usd, source_url
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, h := range holdings {
		_, _ = stmt.ExecContext(ctx,
			f.CIK, f.ReportDate, h.CUSIP, h.AssetClass, h.Ticker, h.IssuerName, h.Shares, h.PrevSharesAdj,
			h.ShareDelta, h.ChangePct, h.Action, h.FilingValueUSD, h.PortfolioWtPct, h.QuarterVWAP,
			h.EstFlowUSD, h.LatestPriceUSD, h.MTMValueUSD, f.SourceURL,
		)
	}
	_ = stmt.Close()
	return tx.Commit()
}

func (r *Repo) ListLatestUSFilings(ctx context.Context) ([]US13FFiling, error) {
	rows, err := r.st.ReadDB().QueryContext(ctx, `
		SELECT f.cik, f.institution_name, f.category, f.accession_number, f.report_date, f.filing_date,
		       f.total_value_usd, f.position_count, f.option_count, f.source_url, f.updated_at
		FROM cap_us_13f_filings f
		JOIN (SELECT cik, MAX(report_date) AS max_dt FROM cap_us_13f_filings GROUP BY cik) m
		  ON f.cik = m.cik AND f.report_date = m.max_dt
		ORDER BY f.total_value_usd DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []US13FFiling
	for rows.Next() {
		var f US13FFiling
		if err := rows.Scan(&f.CIK, &f.InstitutionName, &f.Category, &f.AccessionNumber, &f.ReportDate, &f.FilingDate,
			&f.TotalValueUSD, &f.PositionCount, &f.OptionCount, &f.SourceURL, &f.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, f)
	}
	return list, rows.Err()
}

func (r *Repo) ListUSInstitutionHoldings(ctx context.Context, cik, reportDate, assetClass string) ([]US13FHolding, []string, error) {
	qDates, err := r.st.ReadDB().QueryContext(ctx, `SELECT report_date FROM cap_us_13f_filings WHERE cik = ? ORDER BY report_date DESC LIMIT 8`, cik)
	if err != nil {
		return nil, nil, err
	}
	var dates []string
	for qDates.Next() {
		var d string
		if err := qDates.Scan(&d); err == nil {
			dates = append(dates, d)
		}
	}
	qDates.Close()
	if len(dates) == 0 {
		return nil, nil, nil
	}
	if reportDate == "" {
		reportDate = dates[0]
	}

	q := `
		SELECT h.cik, COALESCE(f.institution_name, h.cik), h.report_date, h.cusip, h.ticker, h.issuer_name,
		       h.asset_class, h.shares, h.prev_shares_adj, h.share_delta, h.change_pct, h.action,
		       h.filing_value_usd, h.portfolio_wt_pct, h.quarter_vwap, h.est_flow_usd,
		       h.latest_price_usd, h.mtm_value_usd, h.source_url
		FROM cap_us_13f_holdings h
		LEFT JOIN cap_us_13f_filings f ON f.cik = h.cik AND f.report_date = h.report_date
		WHERE h.cik = ? AND h.report_date = ?
	`
	args := []any{cik, reportDate}
	if assetClass != "" && assetClass != "ALL" {
		q += ` AND h.asset_class = ?`
		args = append(args, assetClass)
	}
	q += ` ORDER BY h.filing_value_usd DESC, ABS(h.est_flow_usd) DESC LIMIT 200`
	rows, err := r.st.ReadDB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, dates, err
	}
	defer rows.Close()
	var list []US13FHolding
	for rows.Next() {
		var h US13FHolding
		if err := rows.Scan(&h.CIK, &h.InstitutionName, &h.ReportDate, &h.CUSIP, &h.Ticker, &h.IssuerName,
			&h.AssetClass, &h.Shares, &h.PrevSharesAdj, &h.ShareDelta, &h.ChangePct, &h.Action,
			&h.FilingValueUSD, &h.PortfolioWtPct, &h.QuarterVWAP, &h.EstFlowUSD,
			&h.LatestPriceUSD, &h.MTMValueUSD, &h.SourceURL); err != nil {
			return nil, dates, err
		}
		list = append(list, h)
	}
	return list, dates, rows.Err()
}

// ListUSConsensusStocks 聚合全部已跟踪核心机构最新一期正股持仓与净增减持流向（剔除期权）。
func (r *Repo) ListUSConsensusStocks(ctx context.Context, limit int) ([]USStockConsensus, error) {
	if limit <= 0 {
		limit = 60
	}
	rows, err := r.st.ReadDB().QueryContext(ctx, `
		WITH latest_f AS (
			SELECT cik, MAX(report_date) AS max_dt FROM cap_us_13f_filings GROUP BY cik
		)
		SELECT COALESCE(NULLIF(h.ticker,''), h.cusip) AS sym,
		       MAX(h.cusip),
		       MAX(h.issuer_name),
		       MAX(h.report_date),
		       COUNT(DISTINCT CASE WHEN h.shares > 0 THEN h.cik END) AS holder_cnt,
		       SUM(CASE WHEN h.action IN ('NEW','ADD') THEN 1 ELSE 0 END) AS add_cnt,
		       SUM(CASE WHEN h.action IN ('REDUCE','SOLD_OUT') THEN 1 ELSE 0 END) AS red_cnt,
		       SUM(h.filing_value_usd) AS tot_val,
		       SUM(h.est_flow_usd) AS agg_flow,
		       SUM(h.shares) AS tot_sh,
		       SUM(h.share_delta) AS net_delta
		FROM cap_us_13f_holdings h
		JOIN latest_f lf ON h.cik = lf.cik AND h.report_date = lf.max_dt
		WHERE h.asset_class = 'EQUITY'
		GROUP BY sym
		ORDER BY ABS(agg_flow) DESC, tot_val DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []USStockConsensus
	for rows.Next() {
		var c USStockConsensus
		if err := rows.Scan(&c.Ticker, &c.CUSIP, &c.IssuerName, &c.ReportDate, &c.HolderCount,
			&c.AddCount, &c.ReduceCount, &c.TotalFilingUSD, &c.AggregateFlowUSD, &c.TotalShares, &c.NetShareDelta); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

func (r *Repo) ListUSStockByInstitutions(ctx context.Context, symbolOrCusip string) ([]US13FHolding, error) {
	key := strings.ToUpper(strings.TrimSpace(symbolOrCusip))
	rows, err := r.st.ReadDB().QueryContext(ctx, `
		SELECT h.cik, COALESCE(f.institution_name, h.cik), h.report_date, h.cusip, h.ticker, h.issuer_name,
		       h.asset_class, h.shares, h.prev_shares_adj, h.share_delta, h.change_pct, h.action,
		       h.filing_value_usd, h.portfolio_wt_pct, h.quarter_vwap, h.est_flow_usd,
		       h.latest_price_usd, h.mtm_value_usd, h.source_url
		FROM cap_us_13f_holdings h
		LEFT JOIN cap_us_13f_filings f ON f.cik = h.cik AND f.report_date = h.report_date
		WHERE (UPPER(h.ticker) = ? OR UPPER(h.cusip) = ?) AND h.asset_class = 'EQUITY'
		ORDER BY h.report_date DESC, h.filing_value_usd DESC
		LIMIT 120
	`, key, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []US13FHolding
	for rows.Next() {
		var h US13FHolding
		if err := rows.Scan(&h.CIK, &h.InstitutionName, &h.ReportDate, &h.CUSIP, &h.Ticker, &h.IssuerName,
			&h.AssetClass, &h.Shares, &h.PrevSharesAdj, &h.ShareDelta, &h.ChangePct, &h.Action,
			&h.FilingValueUSD, &h.PortfolioWtPct, &h.QuarterVWAP, &h.EstFlowUSD,
			&h.LatestPriceUSD, &h.MTMValueUSD, &h.SourceURL); err != nil {
			return nil, err
		}
		list = append(list, h)
	}
	return list, rows.Err()
}

func (r *Repo) UpsertHKSouthboundBatch(ctx context.Context, list []HKSouthboundDaily) error {
	if len(list) == 0 {
		return nil
	}
	r.st.LockWrite()
	defer r.st.UnlockWrite()
	tx, err := r.st.WriteDB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO cap_hk_southbound_daily (
			code, name, settlement_date, trade_date, shares_held, share_delta, hold_ratio_pct,
			close_price_hkd, vwap_hkd, turnover_hkd, volume_shares, hold_value_hkd_yi, net_flow_hkd_yi,
			source_url, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(code, settlement_date) DO UPDATE SET
			name = excluded.name,
			trade_date = excluded.trade_date,
			shares_held = excluded.shares_held,
			share_delta = excluded.share_delta,
			hold_ratio_pct = excluded.hold_ratio_pct,
			close_price_hkd = CASE WHEN excluded.close_price_hkd > 0 THEN excluded.close_price_hkd ELSE cap_hk_southbound_daily.close_price_hkd END,
			vwap_hkd = CASE WHEN excluded.vwap_hkd > 0 THEN excluded.vwap_hkd ELSE cap_hk_southbound_daily.vwap_hkd END,
			turnover_hkd = excluded.turnover_hkd,
			volume_shares = excluded.volume_shares,
			hold_value_hkd_yi = excluded.hold_value_hkd_yi,
			net_flow_hkd_yi = excluded.net_flow_hkd_yi,
			source_url = excluded.source_url,
			updated_at = excluded.updated_at
	`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	now := time.Now().Unix()
	for _, d := range list {
		_, _ = stmt.ExecContext(ctx,
			d.Code, d.Name, d.SettlementDate, d.TradeDate, d.SharesHeld, d.ShareDelta, d.HoldRatioPct,
			d.ClosePriceHKD, d.VWAPHKD, d.TurnoverHKD, d.VolumeShares, d.HoldValueHKDYi, d.NetFlowHKDYi,
			d.SourceURL, now,
		)
	}
	_ = stmt.Close()
	return tx.Commit()
}

func (r *Repo) SaveHKCCASSSnapshot(ctx context.Context, snap HKCCASSSnapshot, brokers []HKBrokerPosition) error {
	r.st.LockWrite()
	defer r.st.UnlockWrite()
	tx, err := r.st.WriteDB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO cap_hk_ccass_snapshots (
			code, settlement_date, trade_date, total_ccass_shares, southbound_shares, foreign_shares,
			cn_inst_shares, internet_shares, southbound_flow_yi, foreign_clean_flow_yi, foreign_raw_flow_yi,
			cn_inst_flow_yi, internet_flow_yi, anomaly_flag, anomaly_note, source_url, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(code, settlement_date) DO UPDATE SET
			trade_date = excluded.trade_date,
			total_ccass_shares = excluded.total_ccass_shares,
			southbound_shares = excluded.southbound_shares,
			foreign_shares = excluded.foreign_shares,
			cn_inst_shares = excluded.cn_inst_shares,
			internet_shares = excluded.internet_shares,
			southbound_flow_yi = excluded.southbound_flow_yi,
			foreign_clean_flow_yi = excluded.foreign_clean_flow_yi,
			foreign_raw_flow_yi = excluded.foreign_raw_flow_yi,
			cn_inst_flow_yi = excluded.cn_inst_flow_yi,
			internet_flow_yi = excluded.internet_flow_yi,
			anomaly_flag = excluded.anomaly_flag,
			anomaly_note = excluded.anomaly_note,
			source_url = excluded.source_url,
			updated_at = excluded.updated_at
	`, snap.Code, snap.SettlementDate, snap.TradeDate, snap.TotalCCASSShares, snap.SouthboundShares, snap.ForeignShares,
		snap.CNInstShares, snap.InternetShares, snap.SouthboundFlowYi, snap.ForeignCleanFlowYi, snap.ForeignRawFlowYi,
		snap.CNInstFlowYi, snap.InternetFlowYi, snap.AnomalyFlag, snap.AnomalyNote, snap.SourceURL, now)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if len(brokers) > 0 {
		_, _ = tx.ExecContext(ctx, `DELETE FROM cap_hk_broker_top WHERE code = ? AND settlement_date = ?`, snap.Code, snap.SettlementDate)
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO cap_hk_broker_top (code, settlement_date, participant_id, participant_name, cohort, shares, share_delta, hold_ratio_pct, flow_hkd_yi, transfer_flag)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		for _, b := range brokers {
			_, _ = stmt.ExecContext(ctx, snap.Code, snap.SettlementDate, b.ParticipantID, b.ParticipantName, b.Cohort, b.Shares, b.ShareDelta, b.HoldRatioPct, b.FlowHKDYi, b.TransferFlag)
		}
		_ = stmt.Close()
	}
	return tx.Commit()
}

func (r *Repo) ListLatestHKSouthbound(ctx context.Context, limit int) ([]HKSouthboundDaily, string, error) {
	if limit <= 0 {
		limit = 80
	}
	var latestDate string
	if err := r.st.ReadDB().QueryRowContext(ctx, `SELECT COALESCE(MAX(settlement_date), '') FROM cap_hk_southbound_daily`).Scan(&latestDate); err != nil || latestDate == "" {
		return nil, "", err
	}
	rows, err := r.st.ReadDB().QueryContext(ctx, `
		SELECT d.code, d.name, d.settlement_date, d.trade_date, d.shares_held, d.share_delta, d.hold_ratio_pct,
		       d.close_price_hkd, d.vwap_hkd, d.turnover_hkd, d.volume_shares, d.hold_value_hkd_yi, d.net_flow_hkd_yi,
		       COALESCE((SELECT SUM(x.net_flow_hkd_yi) FROM (SELECT net_flow_hkd_yi FROM cap_hk_southbound_daily WHERE code=d.code ORDER BY settlement_date DESC LIMIT 5) x), 0) AS f5,
		       COALESCE((SELECT SUM(x.net_flow_hkd_yi) FROM (SELECT net_flow_hkd_yi FROM cap_hk_southbound_daily WHERE code=d.code ORDER BY settlement_date DESC LIMIT 20) x), 0) AS f20,
		       d.source_url, d.updated_at
		FROM cap_hk_southbound_daily d
		WHERE d.settlement_date = ?
		ORDER BY ABS(d.net_flow_hkd_yi) DESC, d.hold_value_hkd_yi DESC
		LIMIT ?
	`, latestDate, limit)
	if err != nil {
		return nil, latestDate, err
	}
	defer rows.Close()
	var list []HKSouthboundDaily
	for rows.Next() {
		var d HKSouthboundDaily
		if err := rows.Scan(&d.Code, &d.Name, &d.SettlementDate, &d.TradeDate, &d.SharesHeld, &d.ShareDelta, &d.HoldRatioPct,
			&d.ClosePriceHKD, &d.VWAPHKD, &d.TurnoverHKD, &d.VolumeShares, &d.HoldValueHKDYi, &d.NetFlowHKDYi,
			&d.Flow5DHKDYi, &d.Flow20DHKDYi, &d.SourceURL, &d.UpdatedAt); err != nil {
			return nil, latestDate, err
		}
		list = append(list, d)
	}
	return list, latestDate, rows.Err()
}

func (r *Repo) GetHKStockDetail(ctx context.Context, code string) ([]HKSouthboundDaily, []HKCCASSSnapshot, []HKBrokerPosition, error) {
	code = padHKCode(code)
	sbRows, err := r.st.ReadDB().QueryContext(ctx, `
		SELECT code, name, settlement_date, trade_date, shares_held, share_delta, hold_ratio_pct,
		       close_price_hkd, vwap_hkd, turnover_hkd, volume_shares, hold_value_hkd_yi, net_flow_hkd_yi, source_url, updated_at
		FROM cap_hk_southbound_daily WHERE code = ? ORDER BY settlement_date DESC LIMIT 60
	`, code)
	if err != nil {
		return nil, nil, nil, err
	}
	var sb []HKSouthboundDaily
	for sbRows.Next() {
		var d HKSouthboundDaily
		if err := sbRows.Scan(&d.Code, &d.Name, &d.SettlementDate, &d.TradeDate, &d.SharesHeld, &d.ShareDelta, &d.HoldRatioPct,
			&d.ClosePriceHKD, &d.VWAPHKD, &d.TurnoverHKD, &d.VolumeShares, &d.HoldValueHKDYi, &d.NetFlowHKDYi, &d.SourceURL, &d.UpdatedAt); err == nil {
			sb = append(sb, d)
		}
	}
	sbRows.Close()
	for i, j := 0, len(sb)-1; i < j; i, j = i+1, j-1 {
		sb[i], sb[j] = sb[j], sb[i]
	}

	ccRows, err := r.st.ReadDB().QueryContext(ctx, `
		SELECT code, settlement_date, trade_date, total_ccass_shares, southbound_shares, foreign_shares,
		       cn_inst_shares, internet_shares, southbound_flow_yi, foreign_clean_flow_yi, foreign_raw_flow_yi,
		       cn_inst_flow_yi, internet_flow_yi, anomaly_flag, anomaly_note, source_url, updated_at
		FROM cap_hk_ccass_snapshots WHERE code = ? ORDER BY settlement_date DESC LIMIT 30
	`, code)
	if err != nil {
		return sb, nil, nil, err
	}
	var snaps []HKCCASSSnapshot
	for ccRows.Next() {
		var s HKCCASSSnapshot
		if err := ccRows.Scan(&s.Code, &s.SettlementDate, &s.TradeDate, &s.TotalCCASSShares, &s.SouthboundShares, &s.ForeignShares,
			&s.CNInstShares, &s.InternetShares, &s.SouthboundFlowYi, &s.ForeignCleanFlowYi, &s.ForeignRawFlowYi,
			&s.CNInstFlowYi, &s.InternetFlowYi, &s.AnomalyFlag, &s.AnomalyNote, &s.SourceURL, &s.UpdatedAt); err == nil {
			snaps = append(snaps, s)
		}
	}
	ccRows.Close()
	for i, j := 0, len(snaps)-1; i < j; i, j = i+1, j-1 {
		snaps[i], snaps[j] = snaps[j], snaps[i]
	}

	var brokers []HKBrokerPosition
	if len(snaps) > 0 {
		latestSettle := snaps[len(snaps)-1].SettlementDate
		bRows, err := r.st.ReadDB().QueryContext(ctx, `
			SELECT code, settlement_date, participant_id, participant_name, cohort, shares, share_delta, hold_ratio_pct, flow_hkd_yi, transfer_flag
			FROM cap_hk_broker_top WHERE code = ? AND settlement_date = ?
			ORDER BY shares DESC LIMIT 25
		`, code, latestSettle)
		if err == nil {
			for bRows.Next() {
				var b HKBrokerPosition
				if err := bRows.Scan(&b.Code, &b.SettlementDate, &b.ParticipantID, &b.ParticipantName, &b.Cohort,
					&b.Shares, &b.ShareDelta, &b.HoldRatioPct, &b.FlowHKDYi, &b.TransferFlag); err == nil {
					brokers = append(brokers, b)
				}
			}
			bRows.Close()
		}
	}
	return sb, snaps, brokers, nil
}

func (r *Repo) GetPrevHKSouthboundShares(ctx context.Context, code, beforeDate string) (float64, error) {
	var sh float64
	err := r.st.ReadDB().QueryRowContext(ctx, `
		SELECT shares_held FROM cap_hk_southbound_daily
		WHERE code = ? AND settlement_date < ?
		ORDER BY settlement_date DESC LIMIT 1
	`, code, beforeDate).Scan(&sh)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return sh, err
}

func (r *Repo) GetPrevHKBrokers(ctx context.Context, code, beforeDate string) (map[string]float64, float64, error) {
	var prevDate string
	var prevTotal float64
	err := r.st.ReadDB().QueryRowContext(ctx, `
		SELECT settlement_date, total_ccass_shares FROM cap_hk_ccass_snapshots
		WHERE code = ? AND settlement_date < ?
		ORDER BY settlement_date DESC LIMIT 1
	`, code, beforeDate).Scan(&prevDate, &prevTotal)
	if err == sql.ErrNoRows {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	rows, err := r.st.ReadDB().QueryContext(ctx, `
		SELECT participant_id, shares FROM cap_hk_broker_top WHERE code = ? AND settlement_date = ?
	`, code, prevDate)
	if err != nil {
		return nil, prevTotal, err
	}
	defer rows.Close()
	m := make(map[string]float64)
	for rows.Next() {
		var id string
		var sh float64
		if err := rows.Scan(&id, &sh); err == nil {
			m[id] = sh
		}
	}
	return m, prevTotal, nil
}

func padCIK(s string) string {
	s = strings.TrimLeft(strings.TrimSpace(s), "0")
	if s == "" {
		return ""
	}
	for len(s) < 10 {
		s = "0" + s
	}
	return s
}

func padHKCode(s string) string {
	s = strings.TrimSpace(strings.TrimPrefix(strings.ToUpper(s), "HK"))
	s = strings.TrimSuffix(s, ".HK")
	for len(s) < 5 {
		s = "0" + s
	}
	return s
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
