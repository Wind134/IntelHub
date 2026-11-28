import { useEffect, useState } from 'react';
import { Lock, User, KeyRound, Eye, EyeOff, ShieldCheck } from 'lucide-react';
import { apiFetch } from '../api';

interface AuthGateProps {
  onSuccess: (username: string) => void;
}

export function AuthGate({ onSuccess }: AuthGateProps) {
  const [mode, setMode] = useState<'login' | 'register'>('login');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [setupKey, setSetupKey] = useState('');
  const [showPw, setShowPw] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [hint, setHint] = useState('');

  useEffect(() => {
    apiFetch<{ has_users: boolean; requires_setup: boolean }>('/api/auth/status')
      .then((st) => {
        if (st.requires_setup) {
          setMode('register');
          setHint('首次部署尚未建号，请使用服务器密钥（INTEL_API_TOKEN）创建管理员账号');
        } else {
          setHint('与 informationHub 共用加密账号体系，未登录请求一律拒绝');
        }
      })
      .catch(() => {});
  }, []);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    if (username.trim().length < 2) return setError('用户名至少需要 2 个字符');
    if (password.length < 8) return setError('密码长度不能少于 8 位');
    if (mode === 'register' && !setupKey.trim()) return setError('注册新账号需要提供服务器邀请密钥');

    setLoading(true);
    try {
      const endpoint = mode === 'register' ? '/api/auth/register' : '/api/auth/login';
      const payload =
        mode === 'register'
          ? { username: username.trim(), password, setup_key: setupKey.trim() }
          : { username: username.trim(), password };
      const res = await apiFetch<{ user?: { username: string } }>(endpoint, {
        method: 'POST',
        body: JSON.stringify(payload),
      });
      onSuccess(res.user?.username || username.trim());
    } catch (err) {
      setError(err instanceof Error ? err.message : '认证失败');
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center p-4 bg-[var(--bg-canvas)]">
      <div className="w-full max-w-md surface-card p-6 sm:p-8 shadow-xl shadow-slate-900/5">
        <div className="flex items-center gap-3.5 mb-6">
          <img src="/icon-256.png" alt="informationHub" className="w-12 h-12 rounded-2xl shadow-sm" />
          <div>
            <h1 className="text-xl font-bold tracking-tight">informationHub</h1>
            <p className="text-xs text-[var(--text-secondary)] mt-0.5">A股 · 美股 · 港股 机构资金流向看板</p>
          </div>
        </div>

        <div className="grid grid-cols-2 p-1 rounded-xl bg-[var(--bg-subtle)] mb-5">
          <button
            type="button"
            onClick={() => {
              setMode('login');
              setError('');
            }}
            className={`py-2 text-sm font-semibold rounded-lg transition-all ${
              mode === 'login'
                ? 'bg-[var(--bg-surface)] text-blue-600 dark:text-blue-400 shadow-xs'
                : 'text-[var(--text-secondary)]'
            }`}
          >
            账号登录
          </button>
          <button
            type="button"
            onClick={() => {
              setMode('register');
              setError('');
            }}
            className={`py-2 text-sm font-semibold rounded-lg transition-all ${
              mode === 'register'
                ? 'bg-[var(--bg-surface)] text-blue-600 dark:text-blue-400 shadow-xs'
                : 'text-[var(--text-secondary)]'
            }`}
          >
            受控注册
          </button>
        </div>

        <form onSubmit={submit} className="space-y-3.5">
          <div>
            <label className="block text-xs font-medium text-[var(--text-secondary)] mb-1.5">用户名</label>
            <div className="relative">
              <User className="w-4 h-4 text-[var(--text-secondary)] absolute left-3.5 top-1/2 -translate-y-1/2" />
              <input
                type="text"
                autoComplete="username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                placeholder="输入用户名"
                className="w-full pl-10 pr-3.5 py-2.5 text-sm rounded-xl bg-[var(--bg-canvas)] border border-[var(--border-color)] focus:outline-none focus:border-blue-500 transition-colors"
              />
            </div>
          </div>

          <div>
            <label className="block text-xs font-medium text-[var(--text-secondary)] mb-1.5">密码</label>
            <div className="relative">
              <Lock className="w-4 h-4 text-[var(--text-secondary)] absolute left-3.5 top-1/2 -translate-y-1/2" />
              <input
                type={showPw ? 'text' : 'password'}
                autoComplete={mode === 'login' ? 'current-password' : 'new-password'}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="至少 8 位密码"
                className="w-full pl-10 pr-10 py-2.5 text-sm rounded-xl bg-[var(--bg-canvas)] border border-[var(--border-color)] focus:outline-none focus:border-blue-500 transition-colors"
              />
              <button
                type="button"
                onClick={() => setShowPw(!showPw)}
                className="absolute right-3 top-1/2 -translate-y-1/2 text-[var(--text-secondary)]"
              >
                {showPw ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
              </button>
            </div>
          </div>

          {mode === 'register' && (
            <div>
              <label className="block text-xs font-medium text-[var(--text-secondary)] mb-1.5">
                服务器注册密钥（INTEL_API_TOKEN）
              </label>
              <div className="relative">
                <KeyRound className="w-4 h-4 text-[var(--text-secondary)] absolute left-3.5 top-1/2 -translate-y-1/2" />
                <input
                  type="password"
                  value={setupKey}
                  onChange={(e) => setSetupKey(e.target.value)}
                  placeholder="防止公网陌生人注册，仅校验一次"
                  className="w-full pl-10 pr-3.5 py-2.5 text-sm rounded-xl bg-[var(--bg-canvas)] border border-[var(--border-color)] focus:outline-none focus:border-blue-500 transition-colors"
                />
              </div>
            </div>
          )}

          {error && (
            <div className="p-3 rounded-xl bg-rose-500/10 border border-rose-500/30 text-xs text-rose-600 dark:text-rose-400">
              {error}
            </div>
          )}

          <button
            type="submit"
            disabled={loading}
            className="w-full py-2.5 rounded-xl bg-blue-600 hover:bg-blue-700 disabled:opacity-60 text-white text-sm font-semibold transition-all cursor-pointer shadow-sm"
          >
            {loading ? '安全验证中…' : mode === 'register' ? '创建账号并进入看板' : '加密登录'}
          </button>
        </form>

        <div className="mt-5 p-3 rounded-xl bg-blue-500/5 border border-blue-500/15 flex items-start gap-2.5">
          <ShieldCheck className="w-4 h-4 text-blue-600 dark:text-blue-400 shrink-0 mt-0.5" />
          <p className="text-[11.5px] text-[var(--text-secondary)] leading-relaxed">
            {hint || 'PBKDF2-SHA256 哈希鉴权 · HttpOnly 加密会话 · 未登录访问任何接口均返回 401 拒绝。'}
          </p>
        </div>
      </div>
    </div>
  );
}
