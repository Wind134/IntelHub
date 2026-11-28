export interface SyncStatus {
  market: string;
  status: string;
  last_sync_at: number;
  data_as_of: string;
  item_count: number;
  error_msg: string;
  source_label: string;
}

export interface SyncProgress {
  market: string;
  running: boolean;
  stage: string;
  current: number;
  total: number;
  percent: number;
  started_at: number;
  finished_at: number;
  error: string;
}

export interface WatchItem {
  id: number;
  market: 'cn_etf' | 'us_inst' | 'hk_stock';
  code: string;
  name: string;
  category: string;
  exchange: string;
  enabled: boolean;
}

export interface CNETFHolder {
  code: string;
  report_date: string;
  report_type: string;
  holder_name: string;
  shares_held: number;
  hold_ratio_pct: number;
  filing_value_yi: number;
  source_url: string;
}

export interface CNETFSummary {
  code: string;
  name: string;
  category: string;
  exchange: string;
  latest_date: string;
  nav: number;
  close_price: number;
  total_shares: number;
  total_aum_yi: number;
  flow_1d_yi: number;
  flow_5d_yi: number;
  flow_20d_yi: number;
  flow_60d_yi: number;
  vol_ratio_20: number;
  volume_signal: string;
  is_intraday: boolean;
  verify_status: string;
  source_note: string;
  gov_shares: number;
  gov_ratio_pct: number;
  gov_mtm_value_yi: number;
  gov_report_date: string;
  gov_report_type: string;
  gov_holders?: CNETFHolder[];
}

export interface CNFlowTrendPoint {
  trade_date: string;
  daily_flow_yi: number;
  cumulative_yi: number;
  core_daily_flow_yi: number;
  core_cumulative_yi: number;
}

export interface CNETFDaily {
  code: string;
  trade_date: string;
  total_shares: number;
  share_delta: number;
  nav: number;
  close_price: number;
  turnover_rmb: number;
  net_flow_rmb: number;
  vol_ratio_20: number;
  volume_signal: string;
  is_intraday: boolean;
  verify_status: string;
  source_note: string;
}

export interface US13FFiling {
  cik: string;
  institution_name: string;
  category: string;
  accession_number: string;
  report_date: string;
  filing_date: string;
  total_value_usd: number;
  position_count: number;
  option_count: number;
  source_url: string;
}

export interface US13FHolding {
  cik: string;
  institution_name: string;
  report_date: string;
  cusip: string;
  ticker: string;
  issuer_name: string;
  asset_class: 'EQUITY' | 'CALL' | 'PUT';
  shares: number;
  prev_shares_adj: number;
  share_delta: number;
  change_pct: number;
  action: 'NEW' | 'ADD' | 'REDUCE' | 'SOLD_OUT' | 'HOLD';
  filing_value_usd: number;
  portfolio_wt_pct: number;
  quarter_vwap: number;
  est_flow_usd: number;
  latest_price_usd: number;
  mtm_value_usd: number;
  source_url: string;
}

export interface USStockConsensus {
  ticker: string;
  cusip: string;
  issuer_name: string;
  report_date: string;
  holder_count: number;
  add_count: number;
  reduce_count: number;
  total_filing_usd: number;
  aggregate_flow_usd: number;
  total_shares: number;
  net_share_delta: number;
}

export interface HKSouthboundDaily {
  code: string;
  name: string;
  settlement_date: string;
  trade_date: string;
  shares_held: number;
  share_delta: number;
  hold_ratio_pct: number;
  close_price_hkd: number;
  vwap_hkd: number;
  turnover_hkd: number;
  volume_shares: number;
  hold_value_hkd_yi: number;
  net_flow_hkd_yi: number;
  flow_5d_hkd_yi: number;
  flow_20d_hkd_yi: number;
  source_url: string;
}

export interface HKCCASSSnapshot {
  code: string;
  settlement_date: string;
  trade_date: string;
  total_ccass_shares: number;
  southbound_shares: number;
  foreign_shares: number;
  cn_inst_shares: number;
  internet_shares: number;
  southbound_flow_yi: number;
  foreign_clean_flow_yi: number;
  foreign_raw_flow_yi: number;
  cn_inst_flow_yi: number;
  internet_flow_yi: number;
  anomaly_flag: string;
  anomaly_note: string;
  source_url: string;
}

export interface HKBrokerPosition {
  code: string;
  settlement_date: string;
  participant_id: string;
  participant_name: string;
  cohort: 'southbound' | 'foreign_inst' | 'cn_inst' | 'internet_retail' | 'other';
  shares: number;
  share_delta: number;
  hold_ratio_pct: number;
  flow_hkd_yi: number;
  transfer_flag: string;
}

export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(path, {
    credentials: 'include',
    headers: {
      'Content-Type': 'application/json',
      ...(init?.headers ?? {}),
    },
    ...init,
  });
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) {
    const err = new Error(data.error || `HTTP ${resp.status}`) as Error & { status?: number };
    err.status = resp.status;
    throw err;
  }
  return data as T;
}

// 统一三地货币与数量格式化（严格原生本币：RMB / USD / HKD）
export function fmtRMBYi(yi: number, signed = false): string {
  const sign = signed && yi > 0 ? '+' : '';
  if (Math.abs(yi) >= 1) return `${sign}¥${yi.toFixed(2)}亿`;
  const wan = yi * 10000;
  return `${sign}¥${wan.toFixed(0)}万`;
}

export function fmtUSD(usd: number, signed = false): string {
  const abs = Math.abs(usd);
  const sign = usd < 0 ? '-' : signed && usd > 0 ? '+' : '';
  if (abs >= 1e9) return `${sign}$${(abs / 1e9).toFixed(2)}B`;
  if (abs >= 1e6) return `${sign}$${(abs / 1e6).toFixed(2)}M`;
  if (abs >= 1e3) return `${sign}$${(abs / 1e3).toFixed(1)}K`;
  return `${sign}$${abs.toFixed(0)}`;
}

export function fmtHKDYi(yi: number, signed = false): string {
  const sign = signed && yi > 0 ? '+' : '';
  if (Math.abs(yi) >= 1) return `${sign}HK$${yi.toFixed(2)}亿`;
  const wan = yi * 10000;
  return `${sign}HK$${wan.toFixed(0)}万`;
}

export function fmtSharesCN(shares: number): string {
  const abs = Math.abs(shares);
  const sign = shares < 0 ? '-' : '';
  if (abs >= 1e8) return `${sign}${(abs / 1e8).toFixed(2)}亿份`;
  if (abs >= 1e4) return `${sign}${(abs / 1e4).toFixed(1)}万份`;
  return `${sign}${abs.toFixed(0)}份`;
}

export function fmtSharesStock(shares: number): string {
  const abs = Math.abs(shares);
  const sign = shares < 0 ? '-' : '';
  if (abs >= 1e8) return `${sign}${(abs / 1e8).toFixed(2)}亿股`;
  if (abs >= 1e4) return `${sign}${(abs / 1e4).toFixed(1)}万股`;
  return `${sign}${abs.toFixed(0)}股`;
}

// 统一中文金融红涨绿跌语义（流入=红/暖玫瑰红，流出=翡翠绿）
export function flowColorClass(val: number): string {
  if (val > 0.0001) return 'text-rose-600 dark:text-rose-400';
  if (val < -0.0001) return 'text-emerald-600 dark:text-emerald-400';
  return 'text-slate-500 dark:text-slate-400';
}
