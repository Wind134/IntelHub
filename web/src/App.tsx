import React, { useEffect, useState } from 'react';
import {
  Activity,
  BookOpen,
  DollarSign,
  Globe2,
  Landmark,
  LogOut,
  Moon,
  RefreshCw,
  SlidersHorizontal,
  Sun,
} from 'lucide-react';
import { apiFetch } from './api';
import { AuthGate } from './components/AuthGate';
import { MarketKind, ProvenanceDrawer } from './components/ProvenanceDrawer';
import { CNMarketView } from './views/CNMarketView';
import { USMarketView } from './views/USMarketView';
import { HKMarketView } from './views/HKMarketView';
import { WatchlistView } from './views/WatchlistView';

type TabKey = 'cn' | 'us' | 'hk' | 'watch';

interface UserInfo {
  id: number;
  username: string;
}

const NAV_ITEMS: { key: TabKey; label: string; shortLabel: string; currency: string; icon: React.ReactNode }[] = [
  { key: 'cn', label: 'A股 国家队ETF', shortLabel: 'A股 ¥', currency: 'RMB', icon: <Landmark className="w-4 h-4" /> },
  { key: 'us', label: '美股 机构13F', shortLabel: '美股 $', currency: 'USD', icon: <DollarSign className="w-4 h-4" /> },
  { key: 'hk', label: '港股 机构CCASS', shortLabel: '港股 HK$', currency: 'HKD', icon: <Globe2 className="w-4 h-4" /> },
  { key: 'watch', label: '自选与同步', shortLabel: '状态', currency: '', icon: <SlidersHorizontal className="w-4 h-4" /> },
];

export default function App() {
  const [authChecked, setAuthChecked] = useState(false);
  const [user, setUser] = useState<UserInfo | null>(null);
  const [tab, setTab] = useState<TabKey>('cn');
  const [dark, setDark] = useState<boolean>(() => {
    const saved = localStorage.getItem('capital_theme');
    return saved === 'dark';
  });
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [refreshTick, setRefreshTick] = useState(0);

  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark);
    localStorage.setItem('capital_theme', dark ? 'dark' : 'light');
  }, [dark]);

  useEffect(() => {
    apiFetch<{ authenticated: boolean; user: UserInfo }>('/api/auth/me')
      .then((res) => {
        if (res.authenticated && res.user) {
          setUser(res.user);
        } else {
          setUser(null);
        }
      })
      .catch(() => setUser(null))
      .finally(() => setAuthChecked(true));
  }, []);

  const handleLogout = async () => {
    await apiFetch('/api/auth/logout', { method: 'POST' }).catch(() => {});
    setUser(null);
  };

  if (!authChecked) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-[var(--bg-page)] text-slate-400 text-xs">
        正在校验加密会话...
      </div>
    );
  }

  if (!user) {
    return <AuthGate onSuccess={(username) => setUser({ id: 0, username })} />;
  }

  const provenanceMarket: MarketKind = tab === 'us' ? 'us' : tab === 'hk' ? 'hk' : 'cn';

  return (
    <div className="min-h-screen bg-[var(--bg-page)] text-[var(--text-primary)] transition-colors">
      {/* 顶部栏（桌面完整导航 + 手机精简工具栏） */}
      <header className="sticky top-0 z-30 backdrop-blur-md bg-[var(--bg-page)]/85 border-b border-[var(--border-subtle)]">
        <div className="max-w-7xl mx-auto px-3.5 sm:px-6 h-14 flex items-center justify-between gap-2">
          <div className="flex items-center gap-2.5 min-w-0">
            <img
              src="/icon-256.png"
              alt="informationHub"
              className="w-7 h-7 rounded-lg shadow-xs shrink-0"
            />
            <div className="min-w-0">
              <div className="text-sm font-bold tracking-tight truncate">
                三地资金流向看板
              </div>
              <div className="text-[10px] text-slate-400 hidden sm:block truncate">
                informationHub · 机构与国家队官方穿透终端
              </div>
            </div>
          </div>

          {/* 桌面端主导航 */}
          <nav className="hidden md:flex items-center gap-1 p-1 rounded-xl bg-slate-200/60 dark:bg-slate-800/80">
            {NAV_ITEMS.map((item) => (
              <button
                key={item.key}
                onClick={() => setTab(item.key)}
                className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium transition-all ${
                  tab === item.key
                    ? 'bg-[var(--bg-card)] text-slate-900 dark:text-white shadow-xs'
                    : 'text-slate-600 dark:text-slate-400 hover:text-slate-900 dark:hover:text-white'
                }`}
              >
                {item.icon}
                <span>{item.label}</span>
              </button>
            ))}
          </nav>

          {/* 右侧操作按钮 */}
          <div className="flex items-center gap-1.5 shrink-0">
            <button
              onClick={() => setDrawerOpen(true)}
              className="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-xl text-xs font-medium bg-blue-50 text-blue-700 dark:bg-blue-950/60 dark:text-blue-300 hover:opacity-90"
              title="数据来源与计算公式说明"
            >
              <BookOpen className="w-3.5 h-3.5" />
              <span className="hidden sm:inline">来源与公式</span>
              <span className="sm:hidden">公式</span>
            </button>

            <button
              onClick={() => setRefreshTick((t) => t + 1)}
              className="p-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-card)] hover:border-slate-300"
              title="刷新当前视图"
            >
              <RefreshCw className="w-3.5 h-3.5" />
            </button>

            <button
              onClick={() => setDark((d) => !d)}
              className="p-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-card)] hover:border-slate-300"
              title={dark ? '切换至浅色财经风' : '切换至深色终端风'}
            >
              {dark ? <Sun className="w-3.5 h-3.5 text-amber-400" /> : <Moon className="w-3.5 h-3.5" />}
            </button>

            <button
              onClick={handleLogout}
              className="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-xl text-xs border border-[var(--border-subtle)] bg-[var(--bg-card)] text-slate-600 dark:text-slate-300 hover:text-rose-600"
              title={`退出登录 (${user.username})`}
            >
              <LogOut className="w-3.5 h-3.5" />
              <span className="hidden lg:inline">{user.username}</span>
            </button>
          </div>
        </div>
      </header>

      {/* 主内容区域（手机端预留底部导航安全距离） */}
      <main className="max-w-7xl mx-auto px-3.5 sm:px-6 pt-4 pb-24 md:pb-10">
        {tab === 'cn' && <CNMarketView dark={dark} refreshTick={refreshTick} />}
        {tab === 'us' && <USMarketView dark={dark} refreshTick={refreshTick} />}
        {tab === 'hk' && <HKMarketView dark={dark} refreshTick={refreshTick} />}
        {tab === 'watch' && <WatchlistView onSynced={() => setRefreshTick((t) => t + 1)} />}

        <footer className="mt-8 pt-4 border-t border-[var(--border-subtle)] flex flex-col sm:flex-row sm:items-center justify-between gap-2 text-[11px] text-slate-400">
          <div className="flex items-center gap-1.5">
            <Activity className="w-3.5 h-3.5 text-emerald-500" />
            <span>A股(RMB) · 美股(USD) · 港股(HKD) 严格原生本币计价，不跨币种混算</span>
          </div>
          <div>所有公开数据源自交易所及监管官方接口，仅供研究参考，不构成投资建议</div>
        </footer>
      </main>

      {/* 手机端固定底部磨砂导航栏 */}
      <nav className="md:hidden fixed bottom-0 inset-x-0 z-30 backdrop-blur-lg bg-[var(--bg-card)]/92 border-t border-[var(--border-subtle)] safe-pb">
        <div className="grid grid-cols-4 h-14">
          {NAV_ITEMS.map((item) => {
            const active = tab === item.key;
            return (
              <button
                key={item.key}
                onClick={() => setTab(item.key)}
                className={`flex flex-col items-center justify-center gap-0.5 text-[11px] font-medium transition-colors ${
                  active ? 'text-blue-600 dark:text-blue-400' : 'text-slate-500 dark:text-slate-400'
                }`}
              >
                {item.icon}
                <span>{item.shortLabel}</span>
              </button>
            );
          })}
        </div>
      </nav>

      {/* 数据来源与计算公式审计抽屉 */}
      <ProvenanceDrawer
        open={drawerOpen}
        market={provenanceMarket}
        onClose={() => setDrawerOpen(false)}
      />
    </div>
  );
}
