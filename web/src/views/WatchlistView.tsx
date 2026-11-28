import React, { useEffect, useRef, useState } from 'react';
import {
  CheckCircle2,
  Loader2,
  Plus,
  RefreshCw,
  ShieldCheck,
  Trash2,
  XCircle,
} from 'lucide-react';
import { apiFetch, SyncProgress, SyncStatus, WatchItem } from '../api';

interface Props {
  onSynced: () => void;
}

const MARKET_LABELS: Record<string, string> = {
  cn_etf: 'A股 ETF（6位代码，如 510300）',
  us_inst: '美股 13F 机构（10位 CIK，如 0001067983）',
  hk_stock: '港股 CCASS 标的（5位代码，如 00700）',
};

const SYNC_NAMES: Record<string, string> = {
  cn_etf: 'A股国家队 ETF',
  us_13f: '美股顶级机构 SEC 13F-HR',
  hk_flow: '港股南向 + CCASS 四大阵营',
};

const SYNC_SCHEDULES: Record<string, string> = {
  cn_etf: '北京时间：盘中1分钟量比(东财+腾讯CDN兜底) · 19:30/21:30/次晨08:45 份额定稿',
  us_13f: '美东时间 (ET)：周一至周五 08:30 & 18:15 自动巡检（自动适配夏/冬令时）',
  hk_flow: '北京时间：周一至周五 20:30 同步 T+2 交收快照',
};

export const WatchlistView: React.FC<Props> = ({ onSynced }) => {
  const [statuses, setStatuses] = useState<SyncStatus[]>([]);
  const [progressMap, setProgressMap] = useState<Record<string, SyncProgress>>({});
  const [tushareEnabled, setTushareEnabled] = useState(false);
  const [items, setItems] = useState<WatchItem[]>([]);
  const [market, setMarket] = useState<'cn_etf' | 'us_inst' | 'hk_stock'>('cn_etf');
  const [code, setCode] = useState('');
  const [name, setName] = useState('');
  const [category, setCategory] = useState('');
  const [exchange, setExchange] = useState('SH');
  const [nowSec, setNowSec] = useState(() => Math.floor(Date.now() / 1000));
  const [banner, setBanner] = useState<{ kind: 'info' | 'ok' | 'err'; text: string } | null>(null);
  const prevRunningRef = useRef<Record<string, boolean>>({});

  const anyRunning = Object.values(progressMap).some((p) => p?.running);

  const loadOverview = async () => {
    const ov = await apiFetch<{
      statuses: SyncStatus[];
      progress?: Record<string, SyncProgress>;
      tushare_enabled?: boolean;
    }>('/api/capital/overview');
    const nextStatuses = ov.statuses ?? [];
    const nextProg = ov.progress ?? {};
    setStatuses(nextStatuses);
    setProgressMap(nextProg);
    setTushareEnabled(Boolean(ov.tushare_enabled));

    // 检测从运行中 -> 完成的状态跃迁，自动弹出明确的成功/失败回执并刷新看板
    const finishedMsgs: string[] = [];
    const errMsgs: string[] = [];
    for (const mk of ['cn_etf', 'us_13f', 'hk_flow']) {
      const wasRunning = Boolean(prevRunningRef.current[mk]);
      const p = nextProg[mk];
      const isRunning = Boolean(p?.running);
      if (wasRunning && !isRunning && p) {
        const elapsed =
          p.finished_at && p.started_at ? Math.max(1, p.finished_at - p.started_at) : 0;
        const st = nextStatuses.find((s) => s.market === mk);
        if (p.error) {
          errMsgs.push(`${SYNC_NAMES[mk]} 失败：${p.error}`);
        } else {
          finishedMsgs.push(
            `${SYNC_NAMES[mk]} 同步成功（耗时 ${elapsed}s，更新 ${st?.item_count ?? 0} 条，数据截至 ${st?.data_as_of || '--'}）`
          );
        }
      }
      prevRunningRef.current[mk] = isRunning;
    }
    if (errMsgs.length > 0) {
      setBanner({ kind: 'err', text: errMsgs.join('；') });
    } else if (finishedMsgs.length > 0) {
      setBanner({ kind: 'ok', text: finishedMsgs.join('；') });
      onSynced();
    }
  };

  const loadWatchlist = async () => {
    const wl = await apiFetch<{ items: WatchItem[] }>('/api/capital/watchlist');
    setItems(wl.items ?? []);
  };

  useEffect(() => {
    Promise.all([loadOverview(), loadWatchlist()]).catch(() => {});
  }, []);

  // 有后台任务运行时，每 1.2 秒实时轮询进度条与耗时，结束后自动停止轮询
  useEffect(() => {
    if (!anyRunning) return;
    const poll = setInterval(() => {
      setNowSec(Math.floor(Date.now() / 1000));
      loadOverview().catch(() => {});
    }, 1200);
    return () => clearInterval(poll);
  }, [anyRunning]);

  const triggerSync = async (target: string) => {
    setBanner({ kind: 'info', text: '已启动同步任务，正在实时回传进度...' });
    try {
      const res = await apiFetch<{ progress?: Record<string, SyncProgress> }>('/api/capital/sync', {
        method: 'POST',
        body: JSON.stringify({ market: target }),
      });
      if (res.progress) {
        for (const [k, v] of Object.entries(res.progress)) {
          if (v?.running) prevRunningRef.current[k] = true;
        }
        setProgressMap(res.progress);
      }
      setNowSec(Math.floor(Date.now() / 1000));
    } catch (e: any) {
      setBanner({ kind: 'err', text: `触发失败：${e.message}` });
    }
  };

  const handleAdd = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!code.trim() || !name.trim()) return;
    try {
      await apiFetch('/api/capital/watchlist', {
        method: 'POST',
        body: JSON.stringify({
          market,
          code: code.trim(),
          name: name.trim(),
          category: category.trim() || '自定义',
          exchange: market === 'cn_etf' ? exchange : market === 'hk_stock' ? 'HK' : 'US',
          enabled: true,
        }),
      });
      setCode('');
      setName('');
      setCategory('');
      setBanner({ kind: 'ok', text: '已加入监控池，点击上方「单独刷新此市场」即可实时拉取。' });
      await loadWatchlist();
      onSynced();
    } catch (err: any) {
      setBanner({ kind: 'err', text: `添加失败：${err.message}` });
    }
  };

  const handleDelete = async (item: WatchItem) => {
    try {
      await apiFetch(`/api/capital/watchlist/${item.market}/${encodeURIComponent(item.code)}`, {
        method: 'DELETE',
      });
      await loadWatchlist();
      onSynced();
    } catch (err: any) {
      setBanner({ kind: 'err', text: `删除失败：${err.message}` });
    }
  };

  const filteredWatch = items.filter((i) => i.market === market);

  return (
    <div className="space-y-5">
      {/* 数据源同步健康状态与实时进度条 */}
      <div className="p-4 sm:p-5 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)] space-y-3.5">
        <div className="flex flex-wrap items-center justify-between gap-2.5">
          <div>
            <div className="flex items-center gap-2 flex-wrap">
              <h3 className="text-sm sm:text-base font-bold">三地官方数据源同步状态</h3>
              <span
                className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-medium ${
                  tushareEnabled
                    ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-300'
                    : 'bg-slate-100 text-slate-500 dark:bg-slate-800 dark:text-slate-400'
                }`}
              >
                <ShieldCheck className="w-3 h-3" />
                {tushareEnabled ? 'Tushare Pro 三源核验已启用' : '免费官方直连模式'}
              </span>
            </div>
            <p className="text-xs text-slate-400 mt-0.5">
              支持手动一键触发并实时查看同步进度与完成状态，平时按交易日历自动巡检
            </p>
          </div>
          <button
            onClick={() => triggerSync('all')}
            disabled={anyRunning}
            className="inline-flex items-center gap-1.5 px-3.5 py-2 rounded-xl text-xs font-medium bg-slate-900 text-white dark:bg-blue-600 hover:opacity-90 disabled:opacity-50"
          >
            {anyRunning ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <RefreshCw className="w-3.5 h-3.5" />}
            {anyRunning ? '同步进行中...' : '全市场立即同步'}
          </button>
        </div>

        {banner && (
          <div
            className={`p-3 rounded-xl text-xs flex items-start gap-2 ${
              banner.kind === 'ok'
                ? 'bg-emerald-50 dark:bg-emerald-950/40 text-emerald-800 dark:text-emerald-200 border border-emerald-200/70 dark:border-emerald-800/50'
                : banner.kind === 'err'
                ? 'bg-rose-50 dark:bg-rose-950/40 text-rose-700 dark:text-rose-200 border border-rose-200/70 dark:border-rose-800/50'
                : 'bg-blue-50 dark:bg-blue-950/40 text-blue-700 dark:text-blue-200 border border-blue-200/70 dark:border-blue-800/50'
            }`}
          >
            {banner.kind === 'ok' ? (
              <CheckCircle2 className="w-4 h-4 shrink-0 mt-0.5" />
            ) : banner.kind === 'err' ? (
              <XCircle className="w-4 h-4 shrink-0 mt-0.5" />
            ) : (
              <Loader2 className="w-4 h-4 shrink-0 mt-0.5 animate-spin" />
            )}
            <span className="leading-relaxed">{banner.text}</span>
          </div>
        )}

        <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
          {(['cn_etf', 'us_13f', 'hk_flow'] as const).map((mk) => {
            const st = statuses.find((s) => s.market === mk);
            const prog = progressMap[mk];
            const running = Boolean(prog?.running);
            const ok = st?.status === 'ok';
            const syncKey = mk === 'cn_etf' ? 'cn' : mk === 'us_13f' ? 'us' : 'hk';
            const elapsed = running && prog?.started_at ? Math.max(0, nowSec - prog.started_at) : 0;

            return (
              <div
                key={mk}
                className="p-3.5 rounded-xl bg-slate-50/80 dark:bg-slate-900/40 border border-[var(--border-subtle)] flex flex-col justify-between gap-3"
              >
                <div>
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-xs font-semibold">{SYNC_NAMES[mk]}</span>
                    {running ? (
                      <span className="inline-flex items-center gap-1 text-[11px] font-medium text-blue-600 dark:text-blue-400">
                        <Loader2 className="w-3.5 h-3.5 animate-spin" />
                        {prog?.percent ?? 0}%
                      </span>
                    ) : ok ? (
                      <span className="inline-flex items-center gap-1 text-[11px] font-medium text-emerald-600 dark:text-emerald-400">
                        <CheckCircle2 className="w-3.5 h-3.5" />
                        正常
                      </span>
                    ) : (
                      <XCircle className="w-4 h-4 text-amber-500 shrink-0" />
                    )}
                  </div>

                  {/* 实时进度条（运行中或刚完成时可见） */}
                  {running && prog && (
                    <div className="mt-2.5 space-y-1.5">
                      <div className="w-full h-2 rounded-full bg-slate-200 dark:bg-slate-800 overflow-hidden">
                        <div
                          className="h-full rounded-full bg-blue-600 dark:bg-blue-500 transition-all duration-300"
                          style={{ width: `${Math.max(4, prog.percent)}%` }}
                        />
                      </div>
                      <div className="flex items-center justify-between gap-2 text-[11px] text-blue-700 dark:text-blue-300">
                        <span className="truncate">{prog.stage}</span>
                        <span className="tabular-nums shrink-0">{elapsed}s</span>
                      </div>
                    </div>
                  )}

                  {!running && prog && prog.finished_at > 0 && !prog.error && (
                    <div className="mt-2 text-[11px] text-emerald-600 dark:text-emerald-400 font-medium">
                      ✓ 本次同步完成（耗时 {Math.max(1, prog.finished_at - prog.started_at)}s）
                    </div>
                  )}

                  <div className="mt-2 space-y-1 text-[11px] text-slate-500 dark:text-slate-400 tabular-nums">
                    <div>数据截至：{st?.data_as_of || '尚未同步'}</div>
                    <div>已入库标的：{st?.item_count ?? 0} 条</div>
                    <div>
                      上次更新：
                      {st?.last_sync_at ? new Date(st.last_sync_at * 1000).toLocaleString('zh-CN') : '--'}
                    </div>
                    {st?.source_label && <div className="truncate" title={st.source_label}>数据源：{st.source_label}</div>}
                    <div className="text-[10px] text-slate-400 leading-snug pt-0.5">{SYNC_SCHEDULES[mk]}</div>
                    {(prog?.error || st?.error_msg) && (
                      <div className="text-rose-500 break-all">告警：{prog?.error || st?.error_msg}</div>
                    )}
                  </div>
                </div>
                <button
                  onClick={() => triggerSync(syncKey)}
                  disabled={anyRunning}
                  className="w-full py-1.5 rounded-lg text-xs font-medium border border-[var(--border-subtle)] bg-[var(--bg-card)] hover:border-blue-400 disabled:opacity-50 transition-colors"
                >
                  {running ? '同步中...' : '单独刷新此市场'}
                </button>
              </div>
            );
          })}
        </div>
      </div>

      {/* 自选监控池管理 */}
      <div className="p-4 sm:p-5 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)] space-y-4">
        <div>
          <h3 className="text-sm sm:text-base font-bold">自定义追踪池管理</h3>
          <p className="text-xs text-slate-400">
            在默认国家队 ETF / 16 家美股顶级机构 / 港股核心龙头基础上，支持随时增删任意标的
          </p>
        </div>

        <div className="flex flex-wrap gap-1.5">
          {(['cn_etf', 'us_inst', 'hk_stock'] as const).map((m) => (
            <button
              key={m}
              onClick={() => setMarket(m)}
              className={`px-3 py-1.5 rounded-xl text-xs font-medium transition-colors ${
                market === m
                  ? 'bg-slate-900 text-white dark:bg-blue-600'
                  : 'bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300'
              }`}
            >
              {m === 'cn_etf' ? 'A股 ETF' : m === 'us_inst' ? '美股 13F 机构' : '港股 CCASS'} (
              {items.filter((i) => i.market === m).length})
            </button>
          ))}
        </div>

        <form onSubmit={handleAdd} className="grid grid-cols-1 sm:grid-cols-12 gap-2.5">
          <input
            value={code}
            onChange={(e) => setCode(e.target.value)}
            placeholder={MARKET_LABELS[market]}
            className="sm:col-span-4 px-3 py-2 rounded-xl text-xs bg-slate-50 dark:bg-slate-900/60 border border-[var(--border-subtle)] outline-none focus:border-blue-500"
            required
          />
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="显示名称（如 沪深300ETF）"
            className="sm:col-span-3 px-3 py-2 rounded-xl text-xs bg-slate-50 dark:bg-slate-900/60 border border-[var(--border-subtle)] outline-none focus:border-blue-500"
            required
          />
          <input
            value={category}
            onChange={(e) => setCategory(e.target.value)}
            placeholder="分类标签（可选）"
            className="sm:col-span-2 px-3 py-2 rounded-xl text-xs bg-slate-50 dark:bg-slate-900/60 border border-[var(--border-subtle)] outline-none focus:border-blue-500"
          />
          {market === 'cn_etf' && (
            <select
              value={exchange}
              onChange={(e) => setExchange(e.target.value)}
              className="sm:col-span-1 px-2 py-2 rounded-xl text-xs bg-slate-50 dark:bg-slate-900/60 border border-[var(--border-subtle)]"
            >
              <option value="SH">SH</option>
              <option value="SZ">SZ</option>
            </select>
          )}
          <button
            type="submit"
            className={`${
              market === 'cn_etf' ? 'sm:col-span-2' : 'sm:col-span-3'
            } inline-flex items-center justify-center gap-1 px-3 py-2 rounded-xl text-xs font-medium bg-blue-600 text-white hover:bg-blue-700`}
          >
            <Plus className="w-3.5 h-3.5" />
            加入监控池
          </button>
        </form>

        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-2">
          {filteredWatch.map((it) => (
            <div
              key={`${it.market}-${it.code}`}
              className="px-3 py-2.5 rounded-xl bg-slate-50/70 dark:bg-slate-900/40 border border-[var(--border-subtle)] flex items-center justify-between gap-2"
            >
              <div className="min-w-0">
                <div className="text-xs font-medium truncate">{it.name}</div>
                <div className="text-[11px] text-slate-400 tabular-nums">
                  {it.code} · {it.category}
                </div>
              </div>
              <button
                onClick={() => handleDelete(it)}
                className="p-1.5 rounded-lg text-slate-400 hover:text-rose-600 hover:bg-rose-50 dark:hover:bg-rose-950/40 shrink-0"
                title="移出监控池"
              >
                <Trash2 className="w-3.5 h-3.5" />
              </button>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
};
