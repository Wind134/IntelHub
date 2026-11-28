import React, { useEffect, useMemo, useState } from 'react';
import type { EChartsOption } from 'echarts';
import {
  Building,
  ChevronRight,
  ExternalLink,
  ShieldAlert,
  Users,
  X,
} from 'lucide-react';
import {
  apiFetch,
  flowColorClass,
  fmtSharesStock,
  fmtUSD,
  US13FFiling,
  US13FHolding,
  USStockConsensus,
} from '../api';
import { EChartBox } from '../components/EChartBox';
import { MobileSortBar, SortDir, SortOption, SortTh, toggleSort } from '../components/SortControl';

type USSortKey = 'holder_count' | 'total_filing_usd' | 'aggregate_flow_usd';

const US_SORT_OPTIONS: SortOption<USSortKey>[] = [
  { key: 'aggregate_flow_usd', label: '估算净流向' },
  { key: 'total_filing_usd', label: '持仓市值' },
  { key: 'holder_count', label: '持有机构数' },
];

interface Props {
  dark: boolean;
  refreshTick: number;
}

type SubTab = 'consensus' | 'institutions';
type AssetFilter = 'EQUITY' | 'CALL' | 'PUT';

function ActionBadge({ action }: { action: string }) {
  const styles: Record<string, { label: string; cls: string }> = {
    NEW: {
      label: '新进建仓',
      cls: 'bg-rose-100 text-rose-700 dark:bg-rose-950/60 dark:text-rose-300',
    },
    ADD: {
      label: '增持',
      cls: 'bg-rose-50 text-rose-600 dark:bg-rose-950/40 dark:text-rose-300',
    },
    REDUCE: {
      label: '减持',
      cls: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300',
    },
    SOLD_OUT: {
      label: '清仓',
      cls: 'bg-emerald-100 text-emerald-800 dark:bg-emerald-950/70 dark:text-emerald-300',
    },
    HOLD: {
      label: '持平',
      cls: 'bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-400',
    },
  };
  const s = styles[action] ?? styles.HOLD;
  return (
    <span className={`inline-block px-2 py-0.5 rounded-full text-[11px] font-medium whitespace-nowrap ${s.cls}`}>
      {s.label}
    </span>
  );
}

export const USMarketView: React.FC<Props> = ({ dark, refreshTick }) => {
  const [subTab, setSubTab] = useState<SubTab>('consensus');
  const [sortKey, setSortKey] = useState<USSortKey>('aggregate_flow_usd');
  const [sortDir, setSortDir] = useState<SortDir>('desc');
  const [institutions, setInstitutions] = useState<US13FFiling[]>([]);
  const [consensus, setConsensus] = useState<USStockConsensus[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState('');

  // 机构透视状态
  const [activeCik, setActiveCik] = useState('');
  const [assetFilter, setAssetFilter] = useState<AssetFilter>('EQUITY');
  const [reportDates, setReportDates] = useState<string[]>([]);
  const [selectedDate, setSelectedDate] = useState('');
  const [instHoldings, setInstHoldings] = useState<US13FHolding[]>([]);
  const [instLoading, setInstLoading] = useState(false);

  // 个股跨机构持仓弹窗
  const [stockModal, setStockModal] = useState<{ symbol: string; name: string } | null>(null);
  const [stockHoldings, setStockHoldings] = useState<US13FHolding[]>([]);
  const [stockLoading, setStockLoading] = useState(false);

  useEffect(() => {
    let active = true;
    setLoading(true);
    apiFetch<{ institutions: US13FFiling[]; consensus: USStockConsensus[] }>('/api/capital/us/overview')
      .then((res) => {
        if (!active) return;
        const insts = res.institutions ?? [];
        setInstitutions(insts);
        setConsensus(res.consensus ?? []);
        if (insts.length > 0 && !activeCik) {
          setActiveCik(insts[0].cik);
        }
        setErr('');
      })
      .catch((e: Error) => {
        if (active) setErr(e.message);
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [refreshTick]);

  useEffect(() => {
    if (!activeCik || subTab !== 'institutions') return;
    let active = true;
    setInstLoading(true);
    const qs = new URLSearchParams({ asset_class: assetFilter });
    if (selectedDate) qs.set('report_date', selectedDate);
    apiFetch<{ report_dates: string[]; holdings: US13FHolding[] }>(
      `/api/capital/us/institutions/${activeCik}?${qs.toString()}`
    )
      .then((res) => {
        if (!active) return;
        setReportDates(res.report_dates ?? []);
        setInstHoldings(res.holdings ?? []);
      })
      .catch(() => {
        if (active) setInstHoldings([]);
      })
      .finally(() => {
        if (active) setInstLoading(false);
      });
    return () => {
      active = false;
    };
  }, [activeCik, assetFilter, selectedDate, subTab]);

  const openStockModal = (symbol: string, name: string) => {
    setStockModal({ symbol, name });
    setStockLoading(true);
    apiFetch<{ holdings: US13FHolding[] }>(`/api/capital/us/stocks/${encodeURIComponent(symbol)}`)
      .then((res) => setStockHoldings(res.holdings ?? []))
      .catch(() => setStockHoldings([]))
      .finally(() => setStockLoading(false));
  };

  const kpi = useMemo(() => {
    let totalAum = 0;
    let latestRep = '';
    for (const f of institutions) {
      totalAum += f.total_value_usd;
      if (f.report_date > latestRep) latestRep = f.report_date;
    }
    const inflows = consensus.filter((c) => c.aggregate_flow_usd > 0).slice(0, 10);
    const outflows = consensus.filter((c) => c.aggregate_flow_usd < 0).slice(0, 10);
    const topBuy = inflows.reduce((acc, x) => acc + x.aggregate_flow_usd, 0);
    const topSell = outflows.reduce((acc, x) => acc + x.aggregate_flow_usd, 0);
    return { totalAum, latestRep, topBuy, topSell };
  }, [institutions, consensus]);

  const consensusBarOption = useMemo<EChartsOption>(() => {
    const buys = [...consensus]
      .filter((c) => c.aggregate_flow_usd > 0)
      .sort((a, b) => b.aggregate_flow_usd - a.aggregate_flow_usd)
      .slice(0, 7);
    const sells = [...consensus]
      .filter((c) => c.aggregate_flow_usd < 0)
      .sort((a, b) => a.aggregate_flow_usd - b.aggregate_flow_usd)
      .slice(0, 7);
    const rows = [...buys, ...sells];
    const textColor = dark ? '#cbd5e1' : '#334155';
    const gridColor = dark ? '#1e293b' : '#e2e8f0';
    return {
      tooltip: {
        trigger: 'axis',
        axisPointer: { type: 'shadow' },
        formatter: (params: any) => {
          const p = params[0];
          const item = rows[p.dataIndex];
          return `<div style="font-size:12px"><strong>${item.issuer_name}</strong> (${item.ticker})<br/>机构合力估算净流向：<b>${fmtUSD(item.aggregate_flow_usd, true)}</b><br/>增持 ${item.add_count} 家 · 减持 ${item.reduce_count} 家</div>`;
        },
      },
      grid: { left: 8, right: 16, top: 14, bottom: 8, containLabel: true },
      xAxis: {
        type: 'value',
        axisLabel: { color: textColor, fontSize: 10, formatter: (v: number) => `$${v}M` },
        splitLine: { lineStyle: { color: gridColor } },
      },
      yAxis: {
        type: 'category',
        inverse: true,
        data: rows.map((r) => r.issuer_name.slice(0, 14)),
        axisLabel: { color: textColor, fontSize: 11 },
        axisLine: { lineStyle: { color: gridColor } },
      },
      series: [
        {
          type: 'bar',
          barMaxWidth: 15,
          data: rows.map((r) => ({
            value: Number((r.aggregate_flow_usd / 1e6).toFixed(1)),
            itemStyle: {
              color: r.aggregate_flow_usd >= 0 ? '#e11d48' : '#059669',
              borderRadius: r.aggregate_flow_usd >= 0 ? [0, 4, 4, 0] : [4, 0, 0, 4],
            },
          })),
        },
      ],
    };
  }, [consensus, dark]);

  const handleSort = (next: USSortKey) => {
    const res = toggleSort(sortKey, sortDir, next);
    setSortKey(res.key);
    setSortDir(res.dir);
  };

  const sortedConsensus = useMemo(() => {
    const factor = sortDir === 'desc' ? -1 : 1;
    return [...consensus].sort((a, b) => (a[sortKey] - b[sortKey]) * factor);
  }, [consensus, sortKey, sortDir]);

  const activeInst = institutions.find((i) => i.cik === activeCik);

  return (
    <div className="space-y-4">
      {/* 时效与时区提示栏 */}
      <div className="flex flex-wrap items-center justify-between gap-2 px-3.5 py-2 rounded-2xl bg-slate-100/80 dark:bg-slate-900/60 border border-[var(--border-subtle)] text-xs">
        <div className="flex items-center gap-2 text-slate-600 dark:text-slate-300">
          <span className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-indigo-100 text-indigo-700 dark:bg-indigo-950/70 dark:text-indigo-300 whitespace-nowrap">
            季报 13F-HR
          </span>
          <span>每季度结束后 45 日内强制披露 · 按<strong>美东时间 (ET)</strong> 08:30 / 18:15 自动巡检 SEC EDGAR</span>
        </div>
        <span className="text-[11px] text-slate-400">计价单位：美元 USD</span>
      </div>

      {/* KPI */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        <div className="p-3.5 sm:p-4 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)]">
          <div className="flex items-center justify-between text-xs text-slate-500 dark:text-slate-400">
            <span>覆盖顶级机构</span>
            <Building className="w-3.5 h-3.5 text-blue-600 dark:text-blue-400" />
          </div>
          <div className="mt-1.5 text-lg sm:text-xl font-bold tabular-nums">{institutions.length} 家</div>
          <div className="mt-1 text-[11px] text-slate-400">最新报告期 {kpi.latestRep || '待同步'}</div>
        </div>

        <div className="p-3.5 sm:p-4 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)]">
          <div className="text-xs text-slate-500 dark:text-slate-400">机构正股总申报市值</div>
          <div className="mt-1.5 text-lg sm:text-xl font-bold tabular-nums">{fmtUSD(kpi.totalAum)}</div>
          <div className="mt-1 text-[11px] text-slate-400">SEC 13F-HR 法定值 · USD</div>
        </div>

        <div className="p-3.5 sm:p-4 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)]">
          <div className="text-xs text-slate-500 dark:text-slate-400">Top 10 抱团估算流入</div>
          <div className="mt-1.5 text-lg sm:text-xl font-bold tabular-nums text-rose-600 dark:text-rose-400">
            {fmtUSD(kpi.topBuy, true)}
          </div>
          <div className="mt-1 text-[11px] text-slate-400">已剔除 CALL / PUT 期权</div>
        </div>

        <div className="p-3.5 sm:p-4 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)]">
          <div className="text-xs text-slate-500 dark:text-slate-400">Top 10 抱团估算流出</div>
          <div className="mt-1.5 text-lg sm:text-xl font-bold tabular-nums text-emerald-600 dark:text-emerald-400">
            {fmtUSD(kpi.topSell, true)}
          </div>
          <div className="mt-1 text-[11px] text-slate-400">季度均价法估算</div>
        </div>
      </div>

      {/* 子标签 */}
      <div className="inline-flex p-1 rounded-xl bg-slate-200/70 dark:bg-slate-800/90">
        <button
          onClick={() => setSubTab('consensus')}
          className={`px-3.5 py-1.5 rounded-lg text-xs font-medium transition-all ${
            subTab === 'consensus'
              ? 'bg-[var(--bg-card)] text-slate-900 dark:text-white shadow-xs'
              : 'text-slate-500 dark:text-slate-400'
          }`}
        >
          机构抱团正股流向榜
        </button>
        <button
          onClick={() => setSubTab('institutions')}
          className={`px-3.5 py-1.5 rounded-lg text-xs font-medium transition-all ${
            subTab === 'institutions'
              ? 'bg-[var(--bg-card)] text-slate-900 dark:text-white shadow-xs'
              : 'text-slate-500 dark:text-slate-400'
          }`}
        >
          16 家顶级机构单家透视
        </button>
      </div>

      {err && (
        <div className="p-3 rounded-xl bg-rose-50 dark:bg-rose-950/40 text-rose-600 dark:text-rose-300 text-xs">
          加载失败：{err}
        </div>
      )}

      {subTab === 'consensus' ? (
        <>
          {!loading && consensus.length > 0 && (
            <div className="p-4 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)]">
              <h3 className="text-sm font-semibold mb-1">华尔街顶级机构 · 季度正股估算净买卖前 14（美元 $M）</h3>
              <p className="text-[11px] text-slate-400 mb-2">
                仅统计普通股（EQUITY），严格隔离期权；点击下方个股查看巴菲特/段永平/桥水/先锋等全量持仓明细
              </p>
              <EChartBox option={consensusBarOption} dark={dark} height={310} />
            </div>
          )}

          <MobileSortBar options={US_SORT_OPTIONS} activeKey={sortKey} dir={sortDir} onSort={handleSort} />

          {/* 桌面端抱团表格 */}
          <div className="hidden md:block rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)] overflow-hidden">
            <div className="overflow-x-auto">
              <table className="w-full text-sm text-left">
                <thead className="text-xs text-slate-500 dark:text-slate-400 bg-slate-50/80 dark:bg-slate-900/50 border-b border-[var(--border-subtle)]">
                  <tr>
                    <th className="py-3 px-4 font-medium">标的 / CUSIP</th>
                    <SortTh colKey="holder_count" label="持有机构" activeKey={sortKey} dir={sortDir} align="center" onSort={handleSort} />
                    <th className="py-3 px-3 font-medium text-center">增持 / 减持</th>
                    <SortTh colKey="total_filing_usd" label="机构合计持仓市值" activeKey={sortKey} dir={sortDir} onSort={handleSort} />
                    <SortTh colKey="aggregate_flow_usd" label="区间估算净流向" activeKey={sortKey} dir={sortDir} onSort={handleSort} />
                    <th className="py-3 px-4 font-medium text-right">明细</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[var(--border-subtle)]">
                  {loading ? (
                    <tr>
                      <td colSpan={6} className="py-10 text-center text-xs text-slate-400">
                        正在加载 SEC EDGAR 13F-HR 正股聚合数据...
                      </td>
                    </tr>
                  ) : (
                    sortedConsensus.slice(0, 60).map((c) => (
                      <tr
                        key={`${c.cusip}-${c.ticker}`}
                        onClick={() => openStockModal(c.ticker || c.cusip, c.issuer_name)}
                        className="hover:bg-slate-50 dark:hover:bg-slate-800/40 cursor-pointer"
                      >
                        <td className="py-3 px-4">
                          <div className="font-medium">{c.issuer_name}</div>
                          <div className="text-xs text-slate-400 tabular-nums">
                            {c.ticker} · 报告期 {c.report_date}
                          </div>
                        </td>
                        <td className="py-3 px-3 text-center tabular-nums font-medium">{c.holder_count} 家</td>
                        <td className="py-3 px-3 text-center text-xs tabular-nums">
                          <span className="text-rose-600 dark:text-rose-400">+{c.add_count}</span>
                          <span className="mx-1 text-slate-300">/</span>
                          <span className="text-emerald-600 dark:text-emerald-400">-{c.reduce_count}</span>
                        </td>
                        <td className="py-3 px-3 text-right tabular-nums font-medium">
                          {fmtUSD(c.total_filing_usd)}
                        </td>
                        <td className={`py-3 px-3 text-right tabular-nums font-semibold ${flowColorClass(c.aggregate_flow_usd)}`}>
                          {fmtUSD(c.aggregate_flow_usd, true)}
                        </td>
                        <td className="py-3 px-4 text-right text-blue-600 dark:text-blue-400 text-xs">
                          <ChevronRight className="w-4 h-4 inline" />
                        </td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
          </div>

          {/* 手机端堆叠卡片 */}
          <div className="md:hidden space-y-2.5">
            {sortedConsensus.slice(0, 50).map((c) => (
              <button
                key={`${c.cusip}-${c.ticker}`}
                onClick={() => openStockModal(c.ticker || c.cusip, c.issuer_name)}
                className="w-full text-left p-3.5 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)]"
              >
                <div className="flex items-start justify-between gap-2">
                  <div className="min-w-0">
                    <div className="font-semibold text-sm truncate">{c.issuer_name}</div>
                    <div className="text-xs text-slate-400 tabular-nums mt-0.5">
                      {c.ticker} · {c.holder_count} 家持有
                    </div>
                  </div>
                  <div className={`text-sm font-bold tabular-nums shrink-0 ${flowColorClass(c.aggregate_flow_usd)}`}>
                    {fmtUSD(c.aggregate_flow_usd, true)}
                  </div>
                </div>
                <div className="mt-2.5 pt-2 border-t border-[var(--border-subtle)] flex items-center justify-between text-xs">
                  <span className="text-slate-500 dark:text-slate-400 tabular-nums">
                    申报市值 <strong className="text-slate-900 dark:text-slate-100">{fmtUSD(c.total_filing_usd)}</strong>
                  </span>
                  <span className="tabular-nums">
                    <span className="text-rose-600 dark:text-rose-400">增 {c.add_count}</span>
                    <span className="mx-1 text-slate-300">/</span>
                    <span className="text-emerald-600 dark:text-emerald-400">减 {c.reduce_count}</span>
                  </span>
                </div>
              </button>
            ))}
          </div>
        </>
      ) : (
        /* 16 家机构透视 */
        <div className="space-y-3">
          {/* 横向机构选择条 */}
          <div className="flex items-center gap-2 overflow-x-auto pb-1">
            {institutions.map((inst) => (
              <button
                key={inst.cik}
                onClick={() => {
                  setActiveCik(inst.cik);
                  setSelectedDate('');
                }}
                className={`px-3 py-2 rounded-xl text-left whitespace-nowrap border transition-colors shrink-0 ${
                  activeCik === inst.cik
                    ? 'bg-slate-900 text-white border-slate-900 dark:bg-blue-600 dark:border-blue-600'
                    : 'bg-[var(--bg-card)] border-[var(--border-subtle)] text-slate-700 dark:text-slate-200'
                }`}
              >
                <div className="text-xs font-semibold">{inst.institution_name}</div>
                <div className="text-[10px] opacity-75 tabular-nums">{fmtUSD(inst.total_value_usd)}</div>
              </button>
            ))}
          </div>

          {activeInst && (
            <div className="p-4 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)] flex flex-col sm:flex-row sm:items-center justify-between gap-3">
              <div>
                <div className="flex items-center gap-2 flex-wrap">
                  <h3 className="text-sm sm:text-base font-bold">{activeInst.institution_name}</h3>
                  <span className="text-[11px] px-2 py-0.5 rounded-full bg-slate-100 dark:bg-slate-800">
                    {activeInst.category}
                  </span>
                  <span className="text-[11px] text-slate-400 font-mono">CIK {activeInst.cik}</span>
                </div>
                <div className="text-xs text-slate-400 mt-1 flex flex-wrap items-center gap-3 tabular-nums">
                  <span>正股总规模：{fmtUSD(activeInst.total_value_usd)}</span>
                  <span>正股 {activeInst.position_count} 只 · 隔离期权 {activeInst.option_count} 条</span>
                  <span>申报日：{activeInst.filing_date}</span>
                  {activeInst.source_url && (
                    <a
                      href={activeInst.source_url}
                      target="_blank"
                      rel="noreferrer"
                      className="inline-flex items-center gap-1 text-blue-600 dark:text-blue-400 hover:underline"
                    >
                      SEC 原始 XML <ExternalLink className="w-3 h-3" />
                    </a>
                  )}
                </div>
              </div>

              <div className="flex items-center gap-2 flex-wrap">
                {reportDates.length > 0 && (
                  <select
                    value={selectedDate || reportDates[0]}
                    onChange={(e) => setSelectedDate(e.target.value)}
                    className="px-2.5 py-1.5 rounded-xl text-xs bg-slate-50 dark:bg-slate-900 border border-[var(--border-subtle)] tabular-nums"
                  >
                    {reportDates.map((d) => (
                      <option key={d} value={d}>
                        报告期 {d}
                      </option>
                    ))}
                  </select>
                )}

                <div className="inline-flex p-0.5 rounded-xl bg-slate-100 dark:bg-slate-800">
                  {([
                    ['EQUITY', '正股 EQUITY'],
                    ['CALL', '看涨 CALL'],
                    ['PUT', '看跌 PUT'],
                  ] as [AssetFilter, string][]).map(([k, lbl]) => (
                    <button
                      key={k}
                      onClick={() => setAssetFilter(k)}
                      className={`px-2.5 py-1 rounded-lg text-[11px] font-medium transition-colors ${
                        assetFilter === k
                          ? 'bg-[var(--bg-card)] text-slate-900 dark:text-white shadow-xs'
                          : 'text-slate-500 dark:text-slate-400'
                      }`}
                    >
                      {lbl}
                    </button>
                  ))}
                </div>
              </div>
            </div>
          )}

          {assetFilter !== 'EQUITY' && (
            <div className="p-3 rounded-xl bg-amber-50 dark:bg-amber-950/35 border border-amber-200 dark:border-amber-800/50 text-xs text-amber-800 dark:text-amber-200 flex items-center gap-2">
              <ShieldAlert className="w-4 h-4 shrink-0" />
              当前展示为 SEC 13F 独立隔离的 {assetFilter} 期权头寸（名义标的股数），不计入上方正股净流向排行。
            </div>
          )}

          {/* 持仓列表（响应式双端适配） */}
          <div className="rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)] overflow-hidden">
            {instLoading ? (
              <div className="py-10 text-center text-xs text-slate-400">正在读取该机构 13F 持仓明细...</div>
            ) : instHoldings.length === 0 ? (
              <div className="py-10 text-center text-xs text-slate-400">当前分类下无持仓记录</div>
            ) : (
              <>
                <div className="hidden md:block overflow-x-auto">
                  <table className="w-full text-sm text-left">
                    <thead className="text-xs text-slate-500 dark:text-slate-400 bg-slate-50/80 dark:bg-slate-900/50 border-b border-[var(--border-subtle)]">
                      <tr>
                        <th className="py-3 px-4 font-medium">标的 / CUSIP</th>
                        <th className="py-3 px-3 font-medium text-center">操作</th>
                        <th className="py-3 px-3 font-medium text-right">持仓股数(变动)</th>
                        <th className="py-3 px-3 font-medium text-right">申报市值(权重)</th>
                        <th className="py-3 px-4 font-medium text-right">估算净流向(USD)</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-[var(--border-subtle)]">
                      {instHoldings.slice(0, 80).map((h, i) => (
                        <tr
                          key={`${h.cusip}-${i}`}
                          onClick={() => openStockModal(h.ticker || h.cusip, h.issuer_name)}
                          className="hover:bg-slate-50 dark:hover:bg-slate-800/40 cursor-pointer"
                        >
                          <td className="py-2.5 px-4">
                            <div className="font-medium">{h.issuer_name}</div>
                            <div className="text-xs text-slate-400 tabular-nums">
                              {h.ticker || h.cusip} · {h.asset_class}
                            </div>
                          </td>
                          <td className="py-2.5 px-3 text-center">
                            <ActionBadge action={h.action} />
                          </td>
                          <td className="py-2.5 px-3 text-right tabular-nums text-xs">
                            <div>{fmtSharesStock(h.shares)}</div>
                            <div className={flowColorClass(h.share_delta)}>
                              {h.share_delta > 0 ? '+' : ''}
                              {fmtSharesStock(h.share_delta)}
                            </div>
                          </td>
                          <td className="py-2.5 px-3 text-right tabular-nums">
                            <div className="font-medium">{fmtUSD(h.filing_value_usd)}</div>
                            <div className="text-[11px] text-slate-400">占 {h.portfolio_wt_pct.toFixed(2)}%</div>
                          </td>
                          <td className={`py-2.5 px-4 text-right tabular-nums font-semibold ${flowColorClass(h.est_flow_usd)}`}>
                            {fmtUSD(h.est_flow_usd, true)}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>

                <div className="md:hidden divide-y divide-[var(--border-subtle)]">
                  {instHoldings.slice(0, 60).map((h, i) => (
                    <button
                      key={`${h.cusip}-${i}`}
                      onClick={() => openStockModal(h.ticker || h.cusip, h.issuer_name)}
                      className="w-full text-left p-3.5 space-y-2"
                    >
                      <div className="flex items-start justify-between gap-2">
                        <div className="min-w-0">
                          <div className="font-semibold text-sm truncate">{h.issuer_name}</div>
                          <div className="text-xs text-slate-400 tabular-nums">
                            {h.ticker || h.cusip} · 权重 {h.portfolio_wt_pct.toFixed(2)}%
                          </div>
                        </div>
                        <ActionBadge action={h.action} />
                      </div>
                      <div className="flex items-center justify-between text-xs tabular-nums">
                        <span>申报市值 <strong>{fmtUSD(h.filing_value_usd)}</strong></span>
                        <span className={`font-semibold ${flowColorClass(h.est_flow_usd)}`}>
                          流向 {fmtUSD(h.est_flow_usd, true)}
                        </span>
                      </div>
                    </button>
                  ))}
                </div>
              </>
            )}
          </div>
        </div>
      )}

      {/* 个股跨机构持仓透视弹窗 */}
      {stockModal && (
        <div
          className="fixed inset-0 z-50 bg-slate-950/55 backdrop-blur-xs flex items-end sm:items-center justify-center p-0 sm:p-4"
          onClick={() => setStockModal(null)}
        >
          <div
            className="w-full sm:max-w-3xl max-h-[88vh] overflow-y-auto rounded-t-3xl sm:rounded-3xl bg-[var(--bg-card)] border border-[var(--border-subtle)] shadow-2xl p-4 sm:p-6 space-y-4"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-start justify-between gap-3">
              <div>
                <h3 className="text-base sm:text-lg font-bold flex items-center gap-2">
                  <Users className="w-4 h-4 text-blue-600" />
                  {stockModal.name}（{stockModal.symbol}）
                </h3>
                <p className="text-xs text-slate-400 mt-1">
                  追踪池内各华尔街顶级机构 SEC 13F-HR 正股申报持仓与季度增减持变动（USD）
                </p>
              </div>
              <button
                onClick={() => setStockModal(null)}
                className="p-1.5 rounded-xl hover:bg-slate-100 dark:hover:bg-slate-800"
              >
                <X className="w-5 h-5" />
              </button>
            </div>

            {stockLoading ? (
              <div className="py-10 text-center text-xs text-slate-400">正在查询各机构持仓明细...</div>
            ) : stockHoldings.length === 0 ? (
              <div className="py-10 text-center text-xs text-slate-400">暂无跨机构持仓记录</div>
            ) : (
              <div className="space-y-2">
                {stockHoldings.map((h, idx) => (
                  <div
                    key={`${h.cik}-${h.report_date}-${idx}`}
                    className="p-3 rounded-xl bg-slate-50/80 dark:bg-slate-900/40 border border-[var(--border-subtle)] flex flex-col sm:flex-row sm:items-center justify-between gap-2"
                  >
                    <div>
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="text-xs sm:text-sm font-semibold">{h.institution_name}</span>
                        <ActionBadge action={h.action} />
                        <span className="text-[11px] text-slate-400 tabular-nums">{h.report_date}</span>
                      </div>
                      <div className="text-[11px] text-slate-400 mt-0.5 tabular-nums">
                        持仓 {fmtSharesStock(h.shares)} · 组合权重 {h.portfolio_wt_pct.toFixed(2)}%
                      </div>
                    </div>
                    <div className="flex items-center gap-4 text-right tabular-nums">
                      <div>
                        <div className="text-xs font-semibold">{fmtUSD(h.filing_value_usd)}</div>
                        <div className="text-[11px] text-slate-400">申报市值</div>
                      </div>
                      <div className="min-w-[84px]">
                        <div className={`text-xs font-semibold ${flowColorClass(h.est_flow_usd)}`}>
                          {fmtUSD(h.est_flow_usd, true)}
                        </div>
                        <div className="text-[11px] text-slate-400">估算净流向</div>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
};
