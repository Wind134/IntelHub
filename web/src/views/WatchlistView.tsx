import React, { useEffect, useState } from 'react';
import {
  CheckCircle2,
  Loader2,
  Plus,
  RefreshCw,
  Trash2,
  XCircle,
} from 'lucide-react';
import { apiFetch, SyncStatus, WatchItem } from '../api';

interface Props {
  onSynced: () => void;
}

const MARKET_LABELS: Record<string, string> = {
  cn_etf: 'A股 ETF（6位代码，如 510300）',
  us_inst: '美股 13F 机构（10位 CIK，如 0001067983）',
  hk_stock: '港股 CCASS 标的（5位代码，如 00700）',
};

const SYNC_NAMES: Record<string, string> = {
  cn_etf: 'A股国家队 ETF（上交所/东财/腾讯双源）',
  us_13f: '美股顶级机构 SEC 13F-HR（EDGAR 官方 XML）',
  hk_flow: '港股南向 + CCASS 四大阵营（HKEX 官方）',
};

export const WatchlistView: React.FC<Props> = ({ onSynced }) => {
  const [statuses, setStatuses] = useState<SyncStatus[]>([]);
  const [items, setItems] = useState<WatchItem[]>([]);
  const [market, setMarket] = useState<'cn_etf' | 'us_inst' | 'hk_stock'>('cn_etf');
  const [code, setCode] = useState('');
  const [name, setName] = useState('');
  const [category, setCategory] = useState('');
  const [exchange, setExchange] = useState('SH');
  const [busySync, setBusySync] = useState('');
  const [msg, setMsg] = useState('');

  const loadAll = async () => {
    const [ov, wl] = await Promise.all([
      apiFetch<{ statuses: SyncStatus[] }>('/api/capital/overview'),
      apiFetch<{ items: WatchItem[] }>('/api/capital/watchlist'),
    ]);
    setStatuses(ov.statuses ?? []);
    setItems(wl.items ?? []);
  };

  useEffect(() => {
    loadAll().catch(() => {});
  }, []);

  const triggerSync = async (target: string) => {
    setBusySync(target);
    setMsg('');
    try {
      await apiFetch('/api/capital/sync', {
        method: 'POST',
        body: JSON.stringify({ market: target }),
      });
      setMsg(`已触发后台同步任务（${target}），数据将在数秒至数十秒内写入本地库。`);
      setTimeout(() => {
        loadAll().catch(() => {});
        onSynced();
      }, 3000);
    } catch (e: any) {
      setMsg(`触发失败：${e.message}`);
    } finally {
      setBusySync('');
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
      setMsg('已添加至监控池，点击上方同步即可抓取数据。');
      await loadAll();
      onSynced();
    } catch (err: any) {
      setMsg(`添加失败：${err.message}`);
    }
  };

  const handleDelete = async (item: WatchItem) => {
    try {
      await apiFetch(`/api/capital/watchlist/${item.market}/${encodeURIComponent(item.code)}`, {
        method: 'DELETE',
      });
      await loadAll();
      onSynced();
    } catch (err: any) {
      setMsg(`删除失败：${err.message}`);
    }
  };

  const filteredWatch = items.filter((i) => i.market === market);

  return (
    <div className="space-y-5">
      {/* 数据源同步健康状态 */}
      <div className="p-4 sm:p-5 rounded-2xl bg-[var(--bg-card)] border border-[var(--border-subtle)] space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <h3 className="text-sm sm:text-base font-bold">三地官方数据源同步状态</h3>
            <p className="text-xs text-slate-400">
              后台自动按交易日历定时回填，所有展示均读取本地 SQLite 快照，毫秒级响应
            </p>
          </div>
          <button
            onClick={() => triggerSync('all')}
            disabled={busySync !== ''}
            className="inline-flex items-center gap-1.5 px-3.5 py-2 rounded-xl text-xs font-medium bg-slate-900 text-white dark:bg-blue-600 hover:opacity-90 disabled:opacity-50"
          >
            {busySync === 'all' ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <RefreshCw className="w-3.5 h-3.5" />}
            全市场立即同步
          </button>
        </div>

        {msg && (
          <div className="p-2.5 rounded-xl bg-blue-50 dark:bg-blue-950/40 text-blue-700 dark:text-blue-200 text-xs">
            {msg}
          </div>
        )}

        <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
          {(['cn_etf', 'us_13f', 'hk_flow'] as const).map((mk) => {
            const st = statuses.find((s) => s.market === mk);
            const ok = st?.status === 'ok';
            const running = st?.status === 'running';
            const syncKey = mk === 'cn_etf' ? 'cn' : mk === 'us_13f' ? 'us' : 'hk';
            return (
              <div
                key={mk}
                className="p-3.5 rounded-xl bg-slate-50/80 dark:bg-slate-900/40 border border-[var(--border-subtle)] flex flex-col justify-between gap-2.5"
              >
                <div>
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-xs font-semibold">{SYNC_NAMES[mk]}</span>
                    {running ? (
                      <Loader2 className="w-4 h-4 text-blue-500 animate-spin shrink-0" />
                    ) : ok ? (
                      <CheckCircle2 className="w-4 h-4 text-emerald-500 shrink-0" />
                    ) : (
                      <XCircle className="w-4 h-4 text-amber-500 shrink-0" />
                    )}
                  </div>
                  <div className="mt-2 space-y-1 text-[11px] text-slate-500 dark:text-slate-400 tabular-nums">
                    <div>数据截至：{st?.data_as_of || '尚未同步'}</div>
                    <div>已入库标的：{st?.item_count ?? 0} 条</div>
                    <div>
                      上次更新：
                      {st?.last_sync_at ? new Date(st.last_sync_at * 1000).toLocaleString('zh-CN') : '--'}
                    </div>
                    {st?.error_msg && <div className="text-rose-500 break-all">告警：{st.error_msg}</div>}
                  </div>
                </div>
                <button
                  onClick={() => triggerSync(syncKey)}
                  disabled={busySync !== ''}
                  className="w-full py-1.5 rounded-lg text-xs font-medium border border-[var(--border-subtle)] bg-[var(--bg-card)] hover:border-blue-400 transition-colors"
                >
                  单独刷新此市场
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
