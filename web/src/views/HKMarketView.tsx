import React, { useEffect, useMemo, useState } from 'react';
import type { EChartsOption } from 'echarts';
import {
  AlertTriangle,
  Building2,
  ChevronRight,
  Clock,
  Globe2,
  Smartphone,
  X,
} from 'lucide-react';
import {
  apiFetch,
  flowColorClass,
  fmtHKDYi,
  fmtSharesStock,
  HKBrokerPosition,
  HKCCASSSnapshot,
  HKSouthboundDaily,
  WatchItem,
} from '../api';
import { EChartBox } from '../components/EChartBox';

interface Props {
  dark: boolean;
  refreshTick: number;
}

type SubTab = 'southbound' | 'ccass';

const COHORT_META: Record<string, { label: string; color: string; badge: string }> = {
  southbound: {
    label: '南向港股通',
    color: '#e11d48',
    badge: 'bg-rose-50 text-rose-700 dark:bg-rose-950/60 dark:text-rose-300',
  },
  foreign_inst: {
    label: '国际外资大行',
    color: '#2563eb',
    badge: 'bg-blue-50 text-blue-700 dark:bg-blue-950/60 dark:text-blue-300',
  },
  cn_inst: {
    label: '中资在港机构',
    color: '#d97706',
    badge: 'bg-amber-50 text-amber-700 dark:bg-amber-950/60 dark:text-amber-300',
  },
  internet_retail: {
    label: '互联网活跃券商(富途/盈透)',
    color: '#7c3aed',
    badge: 'bg-violet-50 text-violet-700 dark:bg-violet-950/60 dark:text-violet-300',
  },
  other: {
    label: '其他席位',
    color: '#64748b',
    badge: 'bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300',
  },
};

export const HKMarketView: React.FC<Props> = ({ dark, refreshTick }) => {
  const [subTab, setSubTab] = useState<SubTab>('southbound');
  const [items, setItems] = useState<HKSouthboundDaily[]>([]);
  const [watchlist, setWatchlist] = useState<WatchItem[]>([]);
  const [settlementDate, setSettlementDate] = useState('');
  const [tradeDate, setTradeDate] = useState('');
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState('');

  // 个股详情 & CCASS 四大阵营透视
  const [selectedCode, setSelectedCode] = useState<string>('');
  const [selectedName, setSelectedName] = useState<string>('');
  const [detailSB, setDetailSB] = useState<HKSouthboundDaily[]>([]);
  const [detailSnaps, setDetailSnaps] = useState<HKCCASSSnapshot[]>([]);
  const [detailBrokers, setDetailBrokers] = useState<HKBrokerPosition[]>([]);
  const [detailLoading, setDetailLoading] = useState(false);
  const [useCleanForeign, setUseCleanForeign] = useState(true);

  useEffect(() => {
    let active = true;
    setLoading(true);
    apiFetch<{
      settlement_date: string;
      trade_date: string;
      items: HKSouthboundDaily[];
      ccass_watchlist: WatchItem[];
    }>('/api/capital/hk/southbound')
      .then((res) => {
        if (!active) return;
        setItems(res.items ?? []);
        setWatchlist(res.ccass_watchlist ?? []);
        setSettlementDate(res.settlement_date ?? '');
        setTradeDate(res.trade_date ?? '');
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

  const openStockDetail = (code: string, name: string) => {
    setSelectedCode(code);
    setSelectedName(name);
    setDetailLoading(true);
    apiFetch<{
      southbound: HKSouthboundDaily[];
      ccass_snapshots: HKCCASSSnapshot[];
      top_brokers: HKBrokerPosition[];
    }>(`/api/capital/hk/stocks/${code}`)
      .then((res) => {
        setDetailSB(res.southbound ?? []);
        setDetailSnaps(res.ccass_snapshots ?? []);
        setDetailBrokers(res.top_brokers ?? []);
      })
      .catch(() => {
        setDetailSB([]);
        setDetailSnaps([]);
        setDetailBrokers([]);
      })
      .finally(() => setDetailLoading(false));
  };

  const kpi = useMemo(() => {
    let totalHold = 0;
    let net1d = 0;
    let net5d = 0;
    let inflowCount = 0;
    for (const it of items) {
      totalHold += it.hold_value_hkd_yi;
      net1d += it.net_flow_hkd_yi;
      net5d += it.flow_5d_hkd_yi;
      if (it.net_flow_hkd_yi > 0) inflowCount++;
    }
    return { totalHold, net1d, net5d, inflowCount };
  }, [items]);

  const sbBarOption = useMemo<EChartsOption>(() => {
    const sorted = [...items].sort((a, b) => b.net_flow_hkd_yi - a.net_flow_hkd_yi);
    const topIn = sorted.slice(0, 6);
    const topOut = sorted.slice(-6).reverse();
    const merged = [...topIn, ...topOut.filter((x) => !topIn.some((y) => y.code === x.code))];
    const textColor = dark ? '#cbd5e1' : '#334155';
    const gridColor = dark ? '#1e293b' : '#e2e8f0';
    return {
      tooltip: {
        trigger: 'axis',
        axisPointer: { type: 'shadow' },
        formatter: (params: any) => {
          const p = params[0];
          const it = merged[p.dataIndex];
          return `<div style="font-size:12px"><strong>${it.name}</strong> (${it.code}.HK)<br/>南向估算净流向：<b>${fmtHKDYi(p.value, true)}</b><br/>持股比例：${it.hold_ratio_pct.toFixed(2)}%</div>`;
        },
      },
      grid: { left: 8, right: 16, top: 14, bottom: 8, containLabel: true },
      xAxis: {
        type: 'value',
        axisLabel: { color: textColor, fontSize: 11, formatter: '{value}亿' },
        splitLine: { lineStyle: { color: gridColor } },
      },
      yAxis: {
        type: 'category',
        inverse: true,
        data: merged.map((m) => `${m.name.slice(0, 6)}`),
        axisLabel: { color: textColor, fontSize: 11 },
        axisLine: { lineStyle: { color: gridColor } },
      },
      series: [
        {
          type: 'bar',
          barMaxWidth: 16,
          data: merged.map((m) => ({
            value: Number(m.net_flow_hkd_yi.toFixed(2)),
            itemStyle: {
              color: m.net_flow_hkd_yi >= 0 ? '#e11d48' : '#059669',
              borderRadius: m.net_flow_hkd_yi >= 0 ? [0, 4, 4, 0] : [4, 0, 0, 4],
            },
          })),
        },
      ],
    };
  }, [items, dark]);

  const latestSnap = detailSnaps[0];

  const cohortBarOption = useMemo<EChartsOption | null>(() => {
    if (!latestSnap) return null;
    const textColor = dark ? '#cbd5e1' : '#334155';
    const gridColor = dark ? '#1e293b' : '#e2e8f0';
    const foreignVal = useCleanForeign ? latestSnap.foreign_clean_flow_yi : latestSnap.foreign_raw_flow_yi;
    const rows = [
      { name: '南向港股通', value: Number(latestSnap.southbound_flow_yi.toFixed(2)) },
      { name: useCleanForeign ? '外资大行(净)' : '外资大行(原始)', value: Number(foreignVal.toFixed(2)) },
      { name: '中资在港机构', value: Number(latestSnap.cn_inst_flow_yi.toFixed(2)) },
      { name: '互联网券商(富途等)', value: Number(latestSnap.internet_flow_yi.toFixed(2)) },
    ];
    return {
      tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
      grid: { left: 10, right: 16, top: 16, bottom: 12, containLabel: true },
      xAxis: {
        type: 'value',
        axisLabel: { color: textColor, fontSize: 10, formatter: '{value}亿' },
        splitLine: { lineStyle: { color: gridColor } },
      },
      yAxis: {
        type: 'category',
        data: rows.map((r) => r.name),
        axisLabel: { color: textColor, fontSize: 11 },
      },
      series: [
        {
          type: 'bar',
          barMaxWidth: 18,
          data: rows.map((r) => ({
            value: r.value,
            itemStyle: { color: r.value >= 0 ? '#e11d48' : '#059669', borderRadius: 4 },
          })),
        },
      ],
    };
  }, [latestSnap, useCleanForeign, dark]);

  return (
    <div className="space-y-4">
      {/* T+2 日期提示栏 */}
      <div className="flex flex-wrap items-center justify-between gap-2 px-3.5 py-2.5 rounded-2xl bg-blue-50/70 dark:bg-blue-950/30 border border-blue-200/60 dark:border-blue-900/50 text-xs">
        <div className="flex items-center gap-2 flex-wrap text-blue-800 dark:text-blue-200">
          <Clock className="w-3.5 h-3.5 shrink-0" />
          <span className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-blue-100 text-blue-800 dark:bg-blue-900/70 dark:text-blue-200 whitespace-nowrap">
            T+2 交收日更
          </span>
          <span>
            北京时间 20:30 更新 · 当前交收日{' '}
            <strong className="tabular-nums">{settlementDate || '--'}</strong>（对应真实交易日{' '}
            <strong className="tabular-nums">{tradeDate || '--'}</strong>）
          </span>
        </div>
        <span className="text-[11px] text-blue-600 dark:text-blue-300 font-medium">计价单位：港元 HKD</span>
      </div>

      {/* KPI */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        <div className="p-3.5 sm:p-4 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)]">
          <div className="text-xs text-slate-500 dark:text-slate-400">南向榜单总持仓</div>
          <div className="mt-1.5 text-lg sm:text-xl font-bold tabular-nums">{fmtHKDYi(kpi.totalHold)}</div>
          <div className="mt-1 text-[11px] text-slate-400">HKEX 法定持股 × 收盘价</div>
        </div>
        <div className="p-3.5 sm:p-4 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)]">
          <div className="text-xs text-slate-500 dark:text-slate-400">南向当日净流向</div>
          <div className={`mt-1.5 text-lg sm:text-xl font-bold tabular-nums ${flowColorClass(kpi.net1d)}`}>
            {fmtHKDYi(kpi.net1d, true)}
          </div>
          <div className="mt-1 text-[11px] text-slate-400">精确 VWAP 折算（前 120 活跃股）</div>
        </div>
        <div className="p-3.5 sm:p-4 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)]">
          <div className="text-xs text-slate-500 dark:text-slate-400">南向 5 日累计</div>
          <div className={`mt-1.5 text-lg sm:text-xl font-bold tabular-nums ${flowColorClass(kpi.net5d)}`}>
            {fmtHKDYi(kpi.net5d, true)}
          </div>
          <div className="mt-1 text-[11px] text-slate-400">沪深港股通合并去重</div>
        </div>
        <div className="p-3.5 sm:p-4 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)]">
          <div className="flex items-center justify-between text-xs text-slate-500 dark:text-slate-400">
            <span>CCASS 深度监控</span>
            <Smartphone className="w-3.5 h-3.5 text-violet-500" />
          </div>
          <div className="mt-1.5 text-lg sm:text-xl font-bold tabular-nums">{watchlist.length} 只龙头</div>
          <div className="mt-1 text-[11px] text-slate-400">含富途 B01955 / 盈透 / 外资大行</div>
        </div>
      </div>

      {/* 子标签切换 */}
      <div className="inline-flex p-1 rounded-xl bg-slate-200/70 dark:bg-slate-800/90">
        <button
          onClick={() => setSubTab('southbound')}
          className={`px-3.5 py-1.5 rounded-lg text-xs font-medium transition-all ${
            subTab === 'southbound'
              ? 'bg-[var(--bg-card)] text-slate-900 dark:text-white shadow-xs'
              : 'text-slate-500 dark:text-slate-400'
          }`}
        >
          南向港股通流向榜
        </button>
        <button
          onClick={() => setSubTab('ccass')}
          className={`px-3.5 py-1.5 rounded-lg text-xs font-medium transition-all ${
            subTab === 'ccass'
              ? 'bg-[var(--bg-card)] text-slate-900 dark:text-white shadow-xs'
              : 'text-slate-500 dark:text-slate-400'
          }`}
        >
          核心龙头 CCASS 四大阵营（含富途）
        </button>
      </div>

      {err && (
        <div className="p-3 rounded-xl bg-rose-50 dark:bg-rose-950/40 text-rose-600 dark:text-rose-300 text-xs">
          加载失败：{err}
        </div>
      )}

      {subTab === 'southbound' ? (
        <>
          {!loading && items.length > 0 && (
            <div className="p-4 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)]">
              <h3 className="text-sm font-semibold mb-1">南向资金单日净流入 / 净流出前 6（亿港元）</h3>
              <p className="text-[11px] text-slate-400 mb-2">
                采用 HKEX 官方精确股数变动 × 对应真实交易日 VWAP 计算；点击任意标的查看 CCASS 外资与富途席位
              </p>
              <EChartBox option={sbBarOption} dark={dark} height={280} />
            </div>
          )}

          {/* 桌面表格 */}
          <div className="hidden md:block rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)] overflow-hidden">
            <div className="overflow-x-auto">
              <table className="w-full text-sm text-left">
                <thead className="text-xs text-slate-500 dark:text-slate-400 bg-slate-50/80 dark:bg-slate-900/50 border-b border-[var(--border-subtle)]">
                  <tr>
                    <th className="py-3 px-4 font-medium">标的 / 代码</th>
                    <th className="py-3 px-3 font-medium text-right">港股通持仓(占比)</th>
                    <th className="py-3 px-3 font-medium text-right">持股变动</th>
                    <th className="py-3 px-3 font-medium text-right">当日净流向</th>
                    <th className="py-3 px-3 font-medium text-right">5日净流向</th>
                    <th className="py-3 px-4 font-medium text-right">CCASS 席位</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[var(--border-subtle)]">
                  {loading ? (
                    <tr>
                      <td colSpan={6} className="py-10 text-center text-xs text-slate-400">
                        正在加载港股通官方持仓数据...
                      </td>
                    </tr>
                  ) : (
                    items.slice(0, 60).map((it) => (
                      <tr
                        key={it.code}
                        onClick={() => openStockDetail(it.code, it.name)}
                        className="hover:bg-slate-50 dark:hover:bg-slate-800/40 cursor-pointer"
                      >
                        <td className="py-3 px-4">
                          <div className="font-medium">{it.name}</div>
                          <div className="text-xs text-slate-400 tabular-nums">{it.code}.HK</div>
                        </td>
                        <td className="py-3 px-3 text-right tabular-nums">
                          <div className="font-medium">{fmtHKDYi(it.hold_value_hkd_yi)}</div>
                          <div className="text-[11px] text-slate-400">{it.hold_ratio_pct.toFixed(2)}%</div>
                        </td>
                        <td className={`py-3 px-3 text-right tabular-nums text-xs ${flowColorClass(it.share_delta)}`}>
                          {it.share_delta > 0 ? '+' : ''}
                          {fmtSharesStock(it.share_delta)}
                        </td>
                        <td className={`py-3 px-3 text-right tabular-nums font-medium ${flowColorClass(it.net_flow_hkd_yi)}`}>
                          {fmtHKDYi(it.net_flow_hkd_yi, true)}
                        </td>
                        <td className={`py-3 px-3 text-right tabular-nums ${flowColorClass(it.flow_5d_hkd_yi)}`}>
                          {fmtHKDYi(it.flow_5d_hkd_yi, true)}
                        </td>
                        <td className="py-3 px-4 text-right text-xs text-blue-600 dark:text-blue-400">
                          <span className="inline-flex items-center gap-0.5">
                            透视 <ChevronRight className="w-4 h-4" />
                          </span>
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
            {items.slice(0, 50).map((it) => (
              <button
                key={it.code}
                onClick={() => openStockDetail(it.code, it.name)}
                className="w-full text-left p-3.5 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)]"
              >
                <div className="flex items-center justify-between gap-2">
                  <div className="min-w-0">
                    <div className="font-semibold text-sm truncate">{it.name}</div>
                    <div className="text-xs text-slate-400 tabular-nums">
                      {it.code}.HK · 占比 {it.hold_ratio_pct.toFixed(2)}%
                    </div>
                  </div>
                  <div className={`text-sm font-bold tabular-nums ${flowColorClass(it.net_flow_hkd_yi)}`}>
                    {fmtHKDYi(it.net_flow_hkd_yi, true)}
                  </div>
                </div>
                <div className="grid grid-cols-3 gap-2 mt-2.5 pt-2 border-t border-[var(--border-subtle)] text-xs">
                  <div>
                    <span className="text-[11px] text-slate-400 block">南向持仓</span>
                    <span className="font-medium tabular-nums">{fmtHKDYi(it.hold_value_hkd_yi)}</span>
                  </div>
                  <div>
                    <span className="text-[11px] text-slate-400 block">5日流向</span>
                    <span className={`font-medium tabular-nums ${flowColorClass(it.flow_5d_hkd_yi)}`}>
                      {fmtHKDYi(it.flow_5d_hkd_yi, true)}
                    </span>
                  </div>
                  <div className="text-right self-end text-[11px] text-blue-600 dark:text-blue-400">
                    CCASS 明细 →
                  </div>
                </div>
              </button>
            ))}
          </div>
        </>
      ) : (
        /* 核心龙头 CCASS 监控池卡片网格 */
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          {watchlist.map((w) => {
            const sbRow = items.find((i) => i.code === w.code);
            return (
              <button
                key={w.code}
                onClick={() => openStockDetail(w.code, w.name)}
                className="text-left p-4 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)] hover:border-blue-400 dark:hover:border-blue-500 transition-colors"
              >
                <div className="flex items-start justify-between gap-2">
                  <div>
                    <div className="font-semibold text-sm">{w.name}</div>
                    <div className="text-xs text-slate-400 tabular-nums mt-0.5">
                      {w.code}.HK · {w.category}
                    </div>
                  </div>
                  <span className="px-2 py-0.5 rounded-full text-[11px] bg-violet-50 text-violet-700 dark:bg-violet-950/60 dark:text-violet-300 whitespace-nowrap">
                    四大阵营
                  </span>
                </div>
                <div className="mt-3 pt-2.5 border-t border-[var(--border-subtle)] flex items-center justify-between text-xs">
                  <span className="text-slate-400">
                    南向单日：
                    <strong className={`ml-1 tabular-nums ${flowColorClass(sbRow?.net_flow_hkd_yi ?? 0)}`}>
                      {sbRow ? fmtHKDYi(sbRow.net_flow_hkd_yi, true) : '点击查看'}
                    </strong>
                  </span>
                  <span className="text-blue-600 dark:text-blue-400 font-medium">查看富途/外资席位 →</span>
                </div>
              </button>
            );
          })}
        </div>
      )}

      {/* 个股 CCASS 四大阵营 & 前 25 经纪商下钻抽屉 */}
      {selectedCode && (
        <div
          className="fixed inset-0 z-50 bg-slate-950/55 backdrop-blur-xs flex items-end sm:items-center justify-center p-0 sm:p-4"
          onClick={() => setSelectedCode('')}
        >
          <div
            className="w-full sm:max-w-4xl max-h-[90vh] overflow-y-auto rounded-t-3xl sm:rounded-3xl bg-[var(--bg-card)] border border-[var(--border-subtle)] shadow-2xl p-4 sm:p-6 space-y-4"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-start justify-between gap-3">
              <div>
                <div className="flex items-center gap-2 flex-wrap">
                  <h3 className="text-base sm:text-lg font-bold">{selectedName || selectedCode}</h3>
                  <span className="text-xs px-2 py-0.5 rounded-md bg-slate-100 dark:bg-slate-800 tabular-nums">
                    {selectedCode}.HK
                  </span>
                  {latestSnap && (
                    <span className="text-[11px] text-slate-400 tabular-nums">
                      交收日 {latestSnap.settlement_date} / 交易日 {latestSnap.trade_date}
                    </span>
                  )}
                </div>
                <p className="text-xs text-slate-400 mt-1">
                  港交所 CCASS 托管席位严格拆分为四大非重叠阵营（南向 / 国际外资 / 中资机构 / 富途·盈透互联网券商）
                </p>
              </div>
              <button
                onClick={() => setSelectedCode('')}
                className="p-1.5 rounded-xl hover:bg-slate-100 dark:hover:bg-slate-800"
              >
                <X className="w-5 h-5" />
              </button>
            </div>

            {detailLoading ? (
              <div className="py-12 text-center text-xs text-slate-400">正在加载 CCASS 四大阵营与经纪商席位...</div>
            ) : (
              <>
                {latestSnap?.anomaly_flag && (
                  <div className="p-3 rounded-xl bg-amber-50 dark:bg-amber-950/40 border border-amber-200 dark:border-amber-800/60 flex items-start gap-2 text-xs text-amber-800 dark:text-amber-200">
                    <AlertTriangle className="w-4 h-4 shrink-0 mt-0.5" />
                    <div>
                      <strong>托管转仓/存托异动提醒（{latestSnap.anomaly_flag}）</strong>：
                      {latestSnap.anomaly_note || '检测到跨托管行转仓或 ADR 转换，已自动从外资净买卖中剔除噪声。'}
                    </div>
                  </div>
                )}

                {latestSnap ? (
                  <div className="p-3.5 rounded-2xl bg-slate-50/70 dark:bg-slate-900/40 border border-[var(--border-subtle)] space-y-3">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <span className="text-xs font-semibold flex items-center gap-1.5">
                        <Globe2 className="w-3.5 h-3.5 text-blue-600" />
                        四大阵营单日净流向对比（亿港元）
                      </span>
                      <label className="inline-flex items-center gap-1.5 text-[11px] text-slate-500 cursor-pointer">
                        <input
                          type="checkbox"
                          checked={useCleanForeign}
                          onChange={(e) => setUseCleanForeign(e.target.checked)}
                          className="rounded"
                        />
                        自动过滤跨行转仓/ADR噪声
                      </label>
                    </div>
                    {cohortBarOption && <EChartBox option={cohortBarOption} dark={dark} height={190} />}
                  </div>
                ) : (
                  <div className="p-3.5 rounded-xl bg-slate-50 dark:bg-slate-900/40 text-xs text-slate-400">
                    该标的尚未拉取 CCASS 席位快照（可将其加入「港股监控池」后在状态页一键同步）
                  </div>
                )}

                {/* 前 25 经纪商明细（含富途 B01955 专属高亮） */}
                <div>
                  <div className="flex items-center justify-between mb-2">
                    <h4 className="text-sm font-semibold flex items-center gap-1.5">
                      <Building2 className="w-4 h-4 text-slate-500" />
                      CCASS 前 25 大经纪商 / 托管行持仓明细
                    </h4>
                    <span className="text-[11px] text-slate-400">法定精确股数</span>
                  </div>

                  {detailBrokers.length === 0 ? (
                    <div className="p-4 rounded-xl bg-slate-50 dark:bg-slate-900/40 text-xs text-slate-400">
                      暂无单家经纪商明细
                    </div>
                  ) : (
                    <div className="space-y-2">
                      {detailBrokers.map((b) => {
                        const meta = COHORT_META[b.cohort] ?? COHORT_META.other;
                        const isFutu = b.participant_id === 'B01955' || b.participant_name.includes('富途');
                        return (
                          <div
                            key={`${b.participant_id}-${b.participant_name}`}
                            className={`p-3 rounded-xl border flex flex-col sm:flex-row sm:items-center justify-between gap-2 ${
                              isFutu
                                ? 'bg-violet-50/70 dark:bg-violet-950/30 border-violet-300/80 dark:border-violet-800'
                                : 'bg-slate-50/60 dark:bg-slate-900/30 border-[var(--border-subtle)]'
                            }`}
                          >
                            <div className="min-w-0">
                              <div className="flex items-center gap-2 flex-wrap">
                                <span className="text-xs font-mono text-slate-400">{b.participant_id}</span>
                                <span className="text-xs sm:text-sm font-medium truncate">
                                  {b.participant_name}
                                </span>
                                <span className={`px-1.5 py-0.5 rounded text-[10px] font-medium ${meta.badge}`}>
                                  {meta.label}
                                </span>
                                {b.transfer_flag && (
                                  <span className="px-1.5 py-0.5 rounded text-[10px] bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300">
                                    {b.transfer_flag}
                                  </span>
                                )}
                              </div>
                            </div>
                            <div className="flex items-center gap-4 tabular-nums text-right shrink-0">
                              <div>
                                <div className="text-xs font-semibold">{fmtSharesStock(b.shares)}</div>
                                <div className="text-[11px] text-slate-400">占 {b.hold_ratio_pct.toFixed(2)}%</div>
                              </div>
                              <div className="min-w-[82px]">
                                <div className={`text-xs font-semibold ${flowColorClass(b.flow_hkd_yi)}`}>
                                  {fmtHKDYi(b.flow_hkd_yi, true)}
                                </div>
                                <div className="text-[11px] text-slate-400">
                                  {b.share_delta > 0 ? '+' : ''}
                                  {fmtSharesStock(b.share_delta)}
                                </div>
                              </div>
                            </div>
                          </div>
                        );
                      })}
                    </div>
                  )}
                </div>

                {/* 南向近期历史简要 */}
                {detailSB.length > 0 && (
                  <div className="text-[11px] text-slate-400 pt-1">
                    已归档南向港股通连续 {detailSB.length} 个交易日记录，最新收盘价 HK$
                    {detailSB[0].close_price_hkd.toFixed(2)}（VWAP HK${detailSB[0].vwap_hkd.toFixed(2)}）。
                  </div>
                )}
              </>
            )}
          </div>
        </div>
      )}
    </div>
  );
};
