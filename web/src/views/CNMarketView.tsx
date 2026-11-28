import React, { useEffect, useMemo, useState } from 'react';
import type { EChartsOption } from 'echarts';
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  ChevronRight,
  TrendingUp,
  X,
} from 'lucide-react';
import {
  apiFetch,
  CNETFDaily,
  CNETFHolder,
  CNETFSummary,
  CNFlowTrendPoint,
  flowColorClass,
  fmtRMBYi,
  fmtSharesCN,
} from '../api';
import { EChartBox } from '../components/EChartBox';

type WindowKey = '1d' | '5d' | '20d' | '60d';

interface Props {
  dark: boolean;
  refreshTick: number;
}

function getWindowFlow(item: CNETFSummary, win: WindowKey): number {
  switch (win) {
    case '1d':
      return item.flow_1d_yi;
    case '5d':
      return item.flow_5d_yi;
    case '20d':
      return item.flow_20d_yi;
    case '60d':
      return item.flow_60d_yi;
  }
}

function isSZBaselineOnly(etf: CNETFSummary): boolean {
  return (
    etf.exchange === 'SZ' &&
    Math.abs(etf.flow_1d_yi) < 0.0001 &&
    Math.abs(etf.flow_5d_yi) < 0.0001 &&
    Math.abs(etf.flow_20d_yi) < 0.0001
  );
}

function fmtFlowCell(val: number, baselineOnly: boolean): string {
  if (baselineOnly) return '首日留档';
  return fmtRMBYi(val, true);
}

function SignalBadge({ signal, ratio }: { signal: string; ratio: number }) {
  if (signal === 'intraday_surge') {
    return (
      <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-medium bg-amber-100 text-amber-800 dark:bg-amber-950/60 dark:text-amber-300 whitespace-nowrap">
        <Activity className="w-3 h-3" />
        盘中异常放量 {ratio.toFixed(1)}x
      </span>
    );
  }
  if (signal === 'confirmed_inflow') {
    return (
      <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-medium bg-rose-100 text-rose-700 dark:bg-rose-950/60 dark:text-rose-300 whitespace-nowrap">
        <TrendingUp className="w-3 h-3" />
        确认放量净申购 {ratio.toFixed(1)}x
      </span>
    );
  }
  if (signal === 'high_turnover') {
    return (
      <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-medium bg-slate-200 text-slate-700 dark:bg-slate-800 dark:text-slate-300 whitespace-nowrap">
        放量换手 {ratio.toFixed(1)}x
      </span>
    );
  }
  return (
    <span className="text-xs text-slate-400 dark:text-slate-500 tabular-nums">
      {ratio > 0 ? `${ratio.toFixed(2)}x` : '--'}
    </span>
  );
}

function VerifyBadge({ status }: { status: string }) {
  if (status === 'VERIFIED') {
    return (
      <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[11px] bg-emerald-50 text-emerald-700 dark:bg-emerald-950/50 dark:text-emerald-300 whitespace-nowrap">
        <CheckCircle2 className="w-3 h-3" />
        双源核验
      </span>
    );
  }
  if (status === 'WARN') {
    return (
      <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[11px] bg-amber-50 text-amber-700 dark:bg-amber-950/50 dark:text-amber-300 whitespace-nowrap">
        <AlertTriangle className="w-3 h-3" />
        待复核
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[11px] bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-400 whitespace-nowrap">
      官方源
    </span>
  );
}

export const CNMarketView: React.FC<Props> = ({ dark, refreshTick }) => {
  const [items, setItems] = useState<CNETFSummary[]>([]);
  const [trend, setTrend] = useState<CNFlowTrendPoint[]>([]);
  const [trendScope, setTrendScope] = useState<'core' | 'all'>('core');
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState('');
  const [win, setWin] = useState<WindowKey>('5d');
  const [category, setCategory] = useState('全部');
  const [selected, setSelected] = useState<CNETFSummary | null>(null);
  const [detailSeries, setDetailSeries] = useState<CNETFDaily[]>([]);
  const [detailHolders, setDetailHolders] = useState<CNETFHolder[]>([]);
  const [detailLoading, setDetailLoading] = useState(false);

  useEffect(() => {
    let active = true;
    setLoading(true);
    apiFetch<{ currency: string; items: CNETFSummary[]; trend?: CNFlowTrendPoint[] }>('/api/capital/cn/etfs')
      .then((res) => {
        if (!active) return;
        setItems(res.items ?? []);
        setTrend(res.trend ?? []);
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

  const openDetail = (etf: CNETFSummary) => {
    setSelected(etf);
    setDetailLoading(true);
    apiFetch<{ series: CNETFDaily[]; holders: CNETFHolder[] }>(`/api/capital/cn/etfs/${etf.code}`)
      .then((res) => {
        setDetailSeries(res.series ?? []);
        setDetailHolders(res.holders ?? []);
      })
      .catch(() => {
        setDetailSeries([]);
        setDetailHolders([]);
      })
      .finally(() => setDetailLoading(false));
  };

  const categories = useMemo(() => {
    const set = new Set<string>();
    items.forEach((it) => {
      if (it.category) set.add(it.category);
    });
    return ['全部', ...Array.from(set)];
  }, [items]);

  const filtered = useMemo(() => {
    const list = category === '全部' ? items : items.filter((it) => it.category === category);
    return [...list].sort((a, b) => Math.abs(getWindowFlow(b, win)) - Math.abs(getWindowFlow(a, win)));
  }, [items, category, win]);

  const kpi = useMemo(() => {
    let govTotal = 0;
    let flow1d = 0;
    let flowWin = 0;
    let surgeCount = 0;
    let latestDate = '';
    for (const it of items) {
      govTotal += it.gov_mtm_value_yi;
      flow1d += it.flow_1d_yi;
      flowWin += getWindowFlow(it, win);
      if (it.volume_signal === 'intraday_surge' || it.volume_signal === 'confirmed_inflow') {
        surgeCount++;
      }
      if (it.latest_date > latestDate) latestDate = it.latest_date;
    }
    return { govTotal, flow1d, flowWin, surgeCount, latestDate };
  }, [items, win]);

  const barOption = useMemo<EChartsOption>(() => {
    const top = [...filtered]
      .sort((a, b) => getWindowFlow(b, win) - getWindowFlow(a, win))
      .slice(0, 12);
    const names = top.map((e) => e.name.replace('ETF', ''));
    const vals = top.map((e) => Number(getWindowFlow(e, win).toFixed(2)));
    const textColor = dark ? '#cbd5e1' : '#334155';
    const gridColor = dark ? '#1e293b' : '#e2e8f0';
    return {
      tooltip: {
        trigger: 'axis',
        axisPointer: { type: 'shadow' },
        formatter: (params: any) => {
          const p = params[0];
          const item = top[p.dataIndex];
          return `<div style="font-size:12px"><strong>${item.name}</strong> (${item.code})<br/>净申赎：<b>${fmtRMBYi(p.value, true)}</b><br/>国家队重估：${fmtRMBYi(item.gov_mtm_value_yi)}</div>`;
        },
      },
      grid: { left: 8, right: 16, top: 18, bottom: 8, containLabel: true },
      xAxis: {
        type: 'value',
        axisLabel: { color: textColor, fontSize: 11, formatter: '{value}亿' },
        splitLine: { lineStyle: { color: gridColor } },
      },
      yAxis: {
        type: 'category',
        inverse: true,
        data: names,
        axisLabel: { color: textColor, fontSize: 11, width: 88, overflow: 'truncate' },
        axisLine: { lineStyle: { color: gridColor } },
      },
      series: [
        {
          type: 'bar',
          barMaxWidth: 16,
          data: vals.map((v) => ({
            value: v,
            itemStyle: {
              color: v >= 0 ? '#e11d48' : '#059669',
              borderRadius: v >= 0 ? [0, 4, 4, 0] : [4, 0, 0, 4],
            },
          })),
        },
      ],
    };
  }, [filtered, win, dark]);

  const trendChartOption = useMemo<EChartsOption>(() => {
    const dates = trend.map((p) => p.trade_date.slice(5));
    const daily = trend.map((p) =>
      Number((trendScope === 'core' ? p.core_daily_flow_yi : p.daily_flow_yi).toFixed(2))
    );
    const cum = trend.map((p) =>
      Number((trendScope === 'core' ? p.core_cumulative_yi : p.cumulative_yi).toFixed(2))
    );
    const textColor = dark ? '#cbd5e1' : '#334155';
    const gridColor = dark ? '#1e293b' : '#e2e8f0';
    return {
      tooltip: {
        trigger: 'axis',
        axisPointer: { type: 'cross' },
        formatter: (params: any) => {
          const idx = params?.[0]?.dataIndex ?? 0;
          const pt = trend[idx];
          if (!pt) return '';
          const dVal = trendScope === 'core' ? pt.core_daily_flow_yi : pt.daily_flow_yi;
          const cVal = trendScope === 'core' ? pt.core_cumulative_yi : pt.cumulative_yi;
          return `<div style="font-size:12px"><strong>${pt.trade_date}</strong><br/>当日净申赎：<b>${fmtRMBYi(dVal, true)}</b><br/>累计申购趋势：<b>${fmtRMBYi(cVal, true)}</b></div>`;
        },
      },
      legend: {
        data: ['当日净申赎(亿元)', '累计净申赎趋势(亿元)'],
        textStyle: { color: textColor, fontSize: 11 },
      },
      grid: { left: 10, right: 10, top: 32, bottom: 14, containLabel: true },
      xAxis: {
        type: 'category',
        data: dates,
        axisLabel: { color: textColor, fontSize: 10 },
        axisLine: { lineStyle: { color: gridColor } },
      },
      yAxis: [
        {
          type: 'value',
          name: '当日(亿)',
          nameTextStyle: { color: textColor, fontSize: 10 },
          axisLabel: { color: textColor, fontSize: 10 },
          splitLine: { lineStyle: { color: gridColor } },
        },
        {
          type: 'value',
          name: '累计(亿)',
          nameTextStyle: { color: textColor, fontSize: 10 },
          axisLabel: { color: textColor, fontSize: 10 },
          splitLine: { show: false },
        },
      ],
      series: [
        {
          name: '当日净申赎(亿元)',
          type: 'bar',
          barMaxWidth: 16,
          data: daily.map((v) => ({
            value: v,
            itemStyle: {
              color: v >= 0 ? 'rgba(225, 29, 72, 0.78)' : 'rgba(5, 150, 105, 0.78)',
              borderRadius: v >= 0 ? [3, 3, 0, 0] : [0, 0, 3, 3],
            },
          })),
        },
        {
          name: '累计净申赎趋势(亿元)',
          type: 'line',
          yAxisIndex: 1,
          smooth: true,
          symbol: 'circle',
          symbolSize: 5,
          lineStyle: { width: 2.5, color: '#2563eb' },
          itemStyle: { color: '#2563eb' },
          areaStyle: {
            color: dark ? 'rgba(37, 99, 235, 0.16)' : 'rgba(37, 99, 235, 0.10)',
          },
          data: cum,
        },
      ],
    };
  }, [trend, trendScope, dark]);

  const detailChartOption = useMemo<EChartsOption>(() => {
    const sorted = [...detailSeries].sort((a, b) => a.trade_date.localeCompare(b.trade_date));
    const dates = sorted.map((d) => d.trade_date.slice(5));
    const flows = sorted.map((d) => Number(d.net_flow_rmb.toFixed(2)));
    let running = 0;
    const cumFlows = sorted.map((d) => {
      running += d.net_flow_rmb;
      return Number(running.toFixed(2));
    });
    const navs = sorted.map((d) => Number((d.nav || d.close_price).toFixed(4)));
    const textColor = dark ? '#cbd5e1' : '#334155';
    const gridColor = dark ? '#1e293b' : '#e2e8f0';
    return {
      tooltip: { trigger: 'axis' },
      legend: {
        data: ['日净申赎(亿元)', '累计申购趋势(亿元)', '单位净值(NAV)'],
        textStyle: { color: textColor, fontSize: 11 },
      },
      grid: { left: 10, right: 10, top: 34, bottom: 16, containLabel: true },
      xAxis: { type: 'category', data: dates, axisLabel: { color: textColor, fontSize: 10 } },
      yAxis: [
        {
          type: 'value',
          name: '亿元',
          nameTextStyle: { color: textColor, fontSize: 10 },
          axisLabel: { color: textColor, fontSize: 10 },
          splitLine: { lineStyle: { color: gridColor } },
        },
        {
          type: 'value',
          scale: true,
          axisLabel: { color: textColor, fontSize: 10 },
          splitLine: { show: false },
        },
      ],
      series: [
        {
          name: '日净申赎(亿元)',
          type: 'bar',
          data: flows.map((v) => ({
            value: v,
            itemStyle: { color: v >= 0 ? '#e11d48' : '#059669' },
          })),
        },
        {
          name: '累计申购趋势(亿元)',
          type: 'line',
          smooth: true,
          showSymbol: false,
          lineStyle: { width: 2, color: '#f59e0b' },
          itemStyle: { color: '#f59e0b' },
          data: cumFlows,
        },
        {
          name: '单位净值(NAV)',
          type: 'line',
          yAxisIndex: 1,
          smooth: true,
          showSymbol: false,
          lineStyle: { width: 2, color: '#2563eb' },
          itemStyle: { color: '#2563eb' },
          data: navs,
        },
      ],
    };
  }, [detailSeries, dark]);

  const winLabel = win === '1d' ? '当日' : win === '5d' ? '近5日' : win === '20d' ? '近20日' : '近60日';

  return (
    <div className="space-y-4">
      {/* KPI 卡片：手机端 2 列，桌面端 4 列（附清晰时效分层徽章） */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        <div className="p-3.5 sm:p-4 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)] shadow-xs">
          <div className="flex items-center justify-between gap-1 text-xs text-slate-500 dark:text-slate-400">
            <span className="truncate">监控池机构持仓重估</span>
            <span className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300 whitespace-nowrap">
              中报/年报
            </span>
          </div>
          <div className="mt-1.5 text-lg sm:text-xl font-bold tabular-nums tracking-tight">
            {fmtRMBYi(kpi.govTotal)}
          </div>
          <div className="mt-1 text-[11px] text-slate-400">官方定期报告机构占比 × 最新规模</div>
        </div>

        <div className="p-3.5 sm:p-4 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)] shadow-xs">
          <div className="flex items-center justify-between gap-1 text-xs text-slate-500 dark:text-slate-400">
            <span className="truncate">监控池日净申赎</span>
            <span className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-blue-50 text-blue-700 dark:bg-blue-950/60 dark:text-blue-300 whitespace-nowrap">
              收盘日更
            </span>
          </div>
          <div className={`mt-1.5 text-lg sm:text-xl font-bold tabular-nums tracking-tight ${flowColorClass(kpi.flow1d)}`}>
            {fmtRMBYi(kpi.flow1d, true)}
          </div>
          <div className="mt-1 text-[11px] text-slate-400">
            数据日 {kpi.latestDate || '待同步'} · 19:30/次晨08:45(北京)
          </div>
        </div>

        <div className="p-3.5 sm:p-4 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)] shadow-xs">
          <div className="flex items-center justify-between gap-1 text-xs text-slate-500 dark:text-slate-400">
            <span className="truncate">{winLabel}累计净流向</span>
            <span className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-blue-50 text-blue-700 dark:bg-blue-950/60 dark:text-blue-300 whitespace-nowrap">
              收盘日更
            </span>
          </div>
          <div className={`mt-1.5 text-lg sm:text-xl font-bold tabular-nums tracking-tight ${flowColorClass(kpi.flowWin)}`}>
            {fmtRMBYi(kpi.flowWin, true)}
          </div>
          <div className="mt-1 text-[11px] text-slate-400">官方份额变动折算 · RMB</div>
        </div>

        <div className="p-3.5 sm:p-4 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)] shadow-xs">
          <div className="flex items-center justify-between gap-1 text-xs text-slate-500 dark:text-slate-400">
            <span className="truncate">放量异动信号</span>
            <span className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-amber-50 text-amber-700 dark:bg-amber-950/60 dark:text-amber-300 whitespace-nowrap">
              盘中1分钟
            </span>
          </div>
          <div className="mt-1.5 text-lg sm:text-xl font-bold tabular-nums tracking-tight">
            {kpi.surgeCount} <span className="text-xs font-normal text-slate-400">/ {items.length} 只</span>
          </div>
          <div className="mt-1 text-[11px] text-slate-400">交易时段折算量比 ≥ 2.5x(北京)</div>
        </div>
      </div>

      {/* 筛选工具栏 */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2.5">
        <div className="flex items-center gap-1.5 overflow-x-auto pb-1 sm:pb-0">
          {categories.map((cat) => (
            <button
              key={cat}
              onClick={() => setCategory(cat)}
              className={`px-3 py-1.5 rounded-full text-xs font-medium whitespace-nowrap transition-colors ${
                category === cat
                  ? 'bg-slate-900 text-white dark:bg-blue-600'
                  : 'bg-[var(--bg-card)] text-slate-600 dark:text-slate-300 border border-[var(--border-subtle)] hover:border-slate-300'
              }`}
            >
              {cat}
            </button>
          ))}
        </div>

        <div className="inline-flex self-start sm:self-auto p-0.5 rounded-xl bg-slate-200/70 dark:bg-slate-800/90">
          {(['1d', '5d', '20d', '60d'] as WindowKey[]).map((w) => (
            <button
              key={w}
              onClick={() => setWin(w)}
              className={`px-2.5 py-1 rounded-lg text-xs font-medium transition-all ${
                win === w
                  ? 'bg-[var(--bg-card)] text-slate-900 dark:text-white shadow-xs'
                  : 'text-slate-500 dark:text-slate-400'
              }`}
            >
              {w.toUpperCase()}
            </button>
          ))}
        </div>
      </div>

      {err && (
        <div className="p-3 rounded-xl bg-rose-50 dark:bg-rose-950/40 text-rose-600 dark:text-rose-300 text-xs">
          加载失败：{err}
        </div>
      )}

      {/* 图表区：1) 累计净申赎趋势折线图 + 2) 核心宽基 ETF 净申赎排行 */}
      {!loading && trend.length > 0 && (
        <div className="p-4 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)]">
          <div className="flex flex-wrap items-center justify-between gap-2 mb-2">
            <div>
              <h3 className="text-sm font-semibold">
                {trendScope === 'core' ? '国家队核心宽基' : '全监控池 ETF'} · 申购资金累计趋势（人民币亿元）
              </h3>
              <p className="text-[11px] text-slate-400">
                蓝线追踪近 90 个交易日累计净申赎走势（右轴），红绿柱为每日净申赎脉冲（左轴），收盘日更三源核验
              </p>
            </div>
            <div className="inline-flex p-0.5 rounded-xl bg-slate-200/70 dark:bg-slate-800/90">
              <button
                onClick={() => setTrendScope('core')}
                className={`px-2.5 py-1 rounded-lg text-[11px] font-medium transition-all ${
                  trendScope === 'core'
                    ? 'bg-[var(--bg-card)] text-slate-900 dark:text-white shadow-xs'
                    : 'text-slate-500 dark:text-slate-400'
                }`}
              >
                国家队核心宽基
              </button>
              <button
                onClick={() => setTrendScope('all')}
                className={`px-2.5 py-1 rounded-lg text-[11px] font-medium transition-all ${
                  trendScope === 'all'
                    ? 'bg-[var(--bg-card)] text-slate-900 dark:text-white shadow-xs'
                    : 'text-slate-500 dark:text-slate-400'
                }`}
              >
                全部监控池
              </button>
            </div>
          </div>
          <EChartBox option={trendChartOption} dark={dark} height={260} />
        </div>
      )}

      {!loading && filtered.length > 0 && (
        <div className="p-4 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)]">
          <div className="flex items-center justify-between mb-2">
            <div>
              <h3 className="text-sm font-semibold">国家队核心持仓 ETF · {winLabel}净申赎排行（人民币亿元）</h3>
              <p className="text-[11px] text-slate-400">
                红柱代表份额净申购（资金流入），绿柱代表净赎回（资金流出）；点击下方条目可查看日度走势与法定持有人明细
              </p>
            </div>
          </div>
          <EChartBox option={barOption} dark={dark} height={Math.min(340, Math.max(220, filtered.slice(0, 12).length * 26))} />
        </div>
      )}

      {/* 桌面端表格 */}
      <div className="hidden md:block rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)] overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full text-sm text-left">
            <thead className="text-xs text-slate-500 dark:text-slate-400 bg-slate-50/80 dark:bg-slate-900/50 border-b border-[var(--border-subtle)]">
              <tr>
                <th className="py-3 px-4 font-medium">ETF 名称 / 代码</th>
                <th className="py-3 px-3 font-medium text-right">总规模</th>
                <th className="py-3 px-3 font-medium text-right">机构持仓(占比)</th>
                <th className="py-3 px-3 font-medium text-right">当日净申赎</th>
                <th className="py-3 px-3 font-medium text-right">5日净申赎</th>
                <th className="py-3 px-3 font-medium text-right">20日净申赎</th>
                <th className="py-3 px-3 font-medium text-center">量比信号</th>
                <th className="py-3 px-4 font-medium text-right">核验</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[var(--border-subtle)]">
              {loading ? (
                <tr>
                  <td colSpan={8} className="py-10 text-center text-xs text-slate-400">
                    正在加载 A 股国家队 ETF 数据...
                  </td>
                </tr>
              ) : filtered.length === 0 ? (
                <tr>
                  <td colSpan={8} className="py-10 text-center text-xs text-slate-400">
                    暂无数据，请前往「自选与状态」触发首次同步或等待后台回填完成
                  </td>
                </tr>
              ) : (
                filtered.map((etf) => {
                  const szBase = isSZBaselineOnly(etf);
                  return (
                  <tr
                    key={etf.code}
                    onClick={() => openDetail(etf)}
                    className="hover:bg-slate-50 dark:hover:bg-slate-800/40 cursor-pointer transition-colors"
                  >
                    <td className="py-3 px-4">
                      <div className="font-medium text-slate-900 dark:text-slate-100">{etf.name}</div>
                      <div className="text-xs text-slate-400 tabular-nums">
                        {etf.code}.{etf.exchange} · {etf.category}
                      </div>
                    </td>
                    <td className="py-3 px-3 text-right tabular-nums text-xs">
                      {fmtRMBYi(etf.total_aum_yi)}
                    </td>
                    <td className="py-3 px-3 text-right tabular-nums">
                      <div className="font-medium">
                        {etf.gov_ratio_pct > 0 ? fmtRMBYi(etf.gov_mtm_value_yi) : '--'}
                      </div>
                      <div className="text-[11px] text-slate-400">
                        {etf.gov_ratio_pct > 0
                          ? `占 ${etf.gov_ratio_pct.toFixed(1)}% (${etf.gov_report_date.slice(0, 7)})`
                          : '待中报披露'}
                      </div>
                    </td>
                    <td className={`py-3 px-3 text-right tabular-nums font-medium ${szBase ? 'text-slate-400 text-xs font-normal' : flowColorClass(etf.flow_1d_yi)}`}>
                      {fmtFlowCell(etf.flow_1d_yi, szBase)}
                    </td>
                    <td className={`py-3 px-3 text-right tabular-nums font-medium ${szBase ? 'text-slate-400 text-xs font-normal' : flowColorClass(etf.flow_5d_yi)}`}>
                      {szBase ? '--' : fmtRMBYi(etf.flow_5d_yi, true)}
                    </td>
                    <td className={`py-3 px-3 text-right tabular-nums ${szBase ? 'text-slate-400 text-xs' : flowColorClass(etf.flow_20d_yi)}`}>
                      {szBase ? '--' : fmtRMBYi(etf.flow_20d_yi, true)}
                    </td>
                    <td className="py-3 px-3 text-center">
                      <SignalBadge signal={etf.volume_signal} ratio={etf.vol_ratio_20} />
                    </td>
                    <td className="py-3 px-4 text-right">
                      <div className="inline-flex items-center gap-1.5">
                        <VerifyBadge status={etf.verify_status} />
                        <ChevronRight className="w-4 h-4 text-slate-400" />
                      </div>
                    </td>
                  </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* 手机端堆叠卡片列表（严格无挤压排版） */}
      <div className="md:hidden space-y-2.5">
        {loading ? (
          <div className="py-10 text-center text-xs text-slate-400">正在加载 A 股国家队 ETF 数据...</div>
        ) : (
          filtered.map((etf) => (
            <button
              key={etf.code}
              onClick={() => openDetail(etf)}
              className="w-full text-left p-3.5 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)] active:scale-[0.99] transition-transform"
            >
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                  <div className="font-semibold text-sm text-slate-900 dark:text-slate-100 truncate">
                    {etf.name}
                  </div>
                  <div className="text-xs text-slate-400 tabular-nums mt-0.5">
                    {etf.code}.{etf.exchange} · {etf.category}
                  </div>
                </div>
                <VerifyBadge status={etf.verify_status} />
              </div>

              <div className="grid grid-cols-3 gap-2 mt-3 pt-2.5 border-t border-[var(--border-subtle)]">
                <div>
                  <div className="text-[11px] text-slate-400">机构持仓(占比)</div>
                  <div className="text-xs font-semibold tabular-nums mt-0.5">
                    {etf.gov_ratio_pct > 0 ? `${fmtRMBYi(etf.gov_mtm_value_yi)}` : '--'}
                  </div>
                </div>
                <div>
                  <div className="text-[11px] text-slate-400">当日净申赎</div>
                  <div className={`text-xs font-semibold tabular-nums mt-0.5 ${isSZBaselineOnly(etf) ? 'text-slate-400 font-normal' : flowColorClass(etf.flow_1d_yi)}`}>
                    {fmtFlowCell(etf.flow_1d_yi, isSZBaselineOnly(etf))}
                  </div>
                </div>
                <div>
                  <div className="text-[11px] text-slate-400">{winLabel}净流向</div>
                  <div className={`text-xs font-semibold tabular-nums mt-0.5 ${isSZBaselineOnly(etf) ? 'text-slate-400 font-normal' : flowColorClass(getWindowFlow(etf, win))}`}>
                    {isSZBaselineOnly(etf) ? '--' : fmtRMBYi(getWindowFlow(etf, win), true)}
                  </div>
                </div>
              </div>

              {(etf.volume_signal === 'intraday_surge' ||
                etf.volume_signal === 'confirmed_inflow' ||
                etf.volume_signal === 'high_turnover') && (
                <div className="mt-2.5 flex items-center justify-between">
                  <SignalBadge signal={etf.volume_signal} ratio={etf.vol_ratio_20} />
                  <span className="text-[11px] text-blue-600 dark:text-blue-400">查看走势与持仓 →</span>
                </div>
              )}
            </button>
          ))
        )}
      </div>

      {/* 下钻详情抽屉 / 弹窗 */}
      {selected && (
        <div
          className="fixed inset-0 z-50 bg-slate-950/55 backdrop-blur-xs flex items-end sm:items-center justify-center p-0 sm:p-4"
          onClick={() => setSelected(null)}
        >
          <div
            className="w-full sm:max-w-3xl max-h-[90vh] overflow-y-auto rounded-t-3xl sm:rounded-3xl bg-[var(--bg-card)] border border-[var(--border-subtle)] shadow-2xl p-4 sm:p-6 space-y-4"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-start justify-between gap-3">
              <div>
                <div className="flex items-center gap-2 flex-wrap">
                  <h3 className="text-base sm:text-lg font-bold">{selected.name}</h3>
                  <span className="text-xs px-2 py-0.5 rounded-md bg-slate-100 dark:bg-slate-800 tabular-nums">
                    {selected.code}.{selected.exchange}
                  </span>
                  <VerifyBadge status={selected.verify_status} />
                </div>
                <p className="text-xs text-slate-400 mt-1">
                  最新净值 ¥{(selected.nav || selected.close_price).toFixed(4)} · 总份额 {fmtSharesCN(selected.total_shares)} · 总规模 {fmtRMBYi(selected.total_aum_yi)}
                </p>
              </div>
              <button
                onClick={() => setSelected(null)}
                className="p-1.5 rounded-xl hover:bg-slate-100 dark:hover:bg-slate-800"
              >
                <X className="w-5 h-5" />
              </button>
            </div>

            {detailLoading ? (
              <div className="py-12 text-center text-xs text-slate-400">正在加载历史净申赎与国家队法定持仓...</div>
            ) : (
              <>
                <div className="p-3 rounded-2xl bg-slate-50/70 dark:bg-slate-900/40 border border-[var(--border-subtle)]">
                  <div className="text-xs font-medium mb-2">近 90 日官方份额净申赎与单位净值走势（RMB）</div>
                  {detailSeries.length > 0 ? (
                    <EChartBox option={detailChartOption} dark={dark} height={240} />
                  ) : (
                    <div className="py-8 text-center text-xs text-slate-400">暂无日度历史序列</div>
                  )}
                </div>

                <div>
                  <div className="flex items-center justify-between gap-2 mb-1">
                    <h4 className="text-sm font-semibold">定期报告机构持仓结构</h4>
                    <span className="text-[11px] px-2 py-0.5 rounded-full bg-blue-50 text-blue-700 dark:bg-blue-950/60 dark:text-blue-300">
                      中报 / 年报法定披露
                    </span>
                  </div>
                  <p className="text-[11px] text-slate-400 mb-2.5">
                    按证监会信息披露规则，公募 ETF 完整机构/个人持有人结构仅在每年中报（6/30）与年报（12/31）披露，一/三季报不披露全量结构。
                  </p>
                  {detailHolders.length === 0 ? (
                    <div className="p-4 rounded-xl bg-slate-50 dark:bg-slate-900/40 text-xs text-slate-400">
                      该 ETF 当前未在定期报告基准池中登记国家队前十大持有人记录（仍跟踪每日官方份额申赎流向）
                    </div>
                  ) : (
                    <div className="space-y-2">
                      {detailHolders.map((h, idx) => (
                        <div
                          key={`${h.holder_name}-${idx}`}
                          className="p-3 rounded-xl bg-slate-50/80 dark:bg-slate-900/40 border border-[var(--border-subtle)] flex flex-col sm:flex-row sm:items-center justify-between gap-2"
                        >
                          <div>
                            <div className="text-xs sm:text-sm font-medium">{h.holder_name}</div>
                            <div className="text-[11px] text-slate-400 mt-0.5">
                              报告期：{h.report_date}（{h.report_type}）
                            </div>
                          </div>
                          <div className="flex items-center gap-4 text-right tabular-nums">
                            <div>
                              <div className="text-xs font-semibold">{fmtSharesCN(h.shares_held)}</div>
                              <div className="text-[11px] text-slate-400">占总份额 {h.hold_ratio_pct.toFixed(2)}%</div>
                            </div>
                            <div>
                              <div className="text-xs font-semibold text-blue-600 dark:text-blue-400">
                                {h.filing_value_yi > 0
                                  ? fmtRMBYi(h.filing_value_yi)
                                  : fmtRMBYi((h.shares_held * (selected.nav || selected.close_price)) / 1e8)}
                              </div>
                              <div className="text-[11px] text-slate-400">
                                {h.filing_value_yi > 0 ? '报告期末市值' : '按最新净值估算'}
                              </div>
                            </div>
                          </div>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              </>
            )}
          </div>
        </div>
      )}
    </div>
  );
};
