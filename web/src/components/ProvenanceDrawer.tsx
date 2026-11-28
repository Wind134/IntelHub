import { X, ShieldCheck, ExternalLink, Calculator } from 'lucide-react';

export type MarketKind = 'cn' | 'us' | 'hk';

interface ProvenanceDrawerProps {
  open: boolean;
  market: MarketKind;
  onClose: () => void;
}

const METHODOLOGY: Record<
  MarketKind,
  {
    title: string;
    currency: string;
    sources: { name: string; freq: string; badge: string; url: string }[];
    formulas: { label: string; formula: string; note: string }[];
  }
> = {
  cn: {
    title: 'A 股 · 国家队主要持仓 ETF 口径与公式',
    currency: '人民币 RMB（亿元 / 万元）',
    sources: [
      {
        name: '上交所官方 ETF 每日规模接口 + 东财精确总份额(f38) + 腾讯CDN 双源交叉核验',
        freq: '交易日盘后 19:30 & 21:30',
        badge: '官方份额流向',
        url: 'https://www.sse.com.cn/',
      },
      {
        name: '巨潮资讯网 / 证监会法定 ETF 季报(单一持有人≥20%) & 半年报/年报前十持有人',
        freq: '季度 / 半年 / 临时公告',
        badge: '法定精确值',
        url: 'http://www.cninfo.com.cn/',
      },
    ],
    formulas: [
      {
        label: '国家队合计法定份额与动态重估市值',
        formula: 'S_gov(t0) = Σ S_gov,k(t0)  |  V_MTM(t) = S_gov(t0) × NAV(t) / 1e8',
        note: '仅汇总中央汇金投资、汇金资管、证金、外管局旗下平台等法定白名单主体，按当日官方单位净值 NAV(t) 重估。',
      },
      {
        label: 'ETF 每日一级市场真实净申赎资金流（剔除涨跌干扰）',
        formula: 'Flow_ETF(t) = [S_total(t) - S_total(t-1)] × NAV(t) / 1e8',
        note: '使用收盘官方单位净值 NAV(t) 而非二级市场折溢价收盘价；代表该 ETF 全市场一级市场净申赎流向。',
      },
      {
        label: '两阶段异常放量监控（盘中预警 + 盘后份额定性）',
        formula: 'LiveVolRatio = Turnover_live(t,m) / [MA20(Turnover) × (m/240)]',
        note: '盘中 09:30–15:00 按已开盘分钟占比 m/240 折算实时量比 ≥2.5 触发预警；19:30 后叠加 Flow > 0 正式定性为「已确认：显著放量净申购」。',
      },
    ],
  },
  us: {
    title: '美股 · SEC 13F 顶级机构持仓口径与公式',
    currency: '美元 USD（B 十亿 / M 百万）',
    sources: [
      {
        name: '美国证监会 SEC EDGAR 官方 Submissions JSON & Form 13F-HR 原始 XML',
        freq: '季末后 45 天内法定申报 · 每日 06:30 自动巡检',
        badge: '法定精确值',
        url: 'https://www.sec.gov/edgar/searchedgar/companysearch',
      },
    ],
    formulas: [
      {
        label: '正股与期权严格隔离 + 量纲自适应校验',
        formula: '主表仅汇总 AssetClass = EQUITY（CALL / PUT 单独分栏隔离）',
        note: '彻底避免期权名义本金混入正股虚增市值；通过正股中位数隐含股价自动识别历史“千美元”与 2023+“1美元”单位差异。',
      },
      {
        label: '拆股校准后真实增减持与季度估算净流向',
        formula: 'ΔN(q) = N(q) - N(q-1) × SplitFactor  |  EstFlow(q) = ΔN(q) × P_avg(q)',
        note: '自动检测常见整数拆股避免误判加仓；P_avg(q) 采用相邻两季 SEC 审计隐含均价中值，绝不将股价涨跌计入资金流入。',
      },
      {
        label: '全机构池个股合力净流向',
        formula: 'AggregateInstFlow_j(q) = Σ EstFlow_i,j(q)',
        note: '汇总 16 家核心顶级机构（含巴菲特、段永平、先锋、贝莱德、桥水等）同季度的正股净买入/卖出合力。',
      },
    ],
  },
  hk: {
    title: '港股 · 南向港股通 + CCASS 四大阵营口径与公式',
    currency: '港元 HKD（亿港元 / 万港元）',
    sources: [
      {
        name: '港交所披露易《沪深港通持股纪录》官方日度全量表',
        freq: '每日盘后 20:30 自动同步',
        badge: '官方份额流向',
        url: 'https://www3.hkexnews.hk/sdw/search/mutualmarket_c.aspx?t=hk',
      },
      {
        name: '港交所披露易《中央结算系统持股纪录 (CCASS)》（含富途 B01955 / 盈透 B01590 及外资大行）',
        freq: '每日盘后核心龙头快照',
        badge: '官方存管明细',
        url: 'https://www3.hkexnews.hk/sdw/search/searchsdw_c.aspx',
      },
    ],
    formulas: [
      {
        label: 'T+2 交收日对齐与全天真实市场均价 VWAP',
        formula: 'T(D) = D_settle - 2个工作日  |  VWAP(T) = Turnover_HKD(T) / Volume(T)',
        note: '港股 CCASS 反映 T+2 交收后持股，流向严格匹配对应交易日 T 的真实成交均价（总成交额÷总股数）。',
      },
      {
        label: 'CCASS 四大非重叠阵营（含富途/盈透）',
        formula: '① 南向港股通  ② 国际外资大行  ③ 中资在港机构  ④ 互联网/活跃券商(富途B01955/盈透B01590等)',
        note: '单只核心龙头同时保留前 25 大单家经纪商明细，支持穿透查看富途、汇丰、花旗、摩根大通每日精确股数增减。',
      },
      {
        label: '跨席位转仓 & ADR 转换自动识别过滤',
        formula: '若 |ΔN_A + ΔN_B|/max ≤ 5% 且 > 50%全天成交量 => 标记[跨席位转仓]并剔除',
        note: '若全市场 CCASS 存管总量单日跳变 > 80% 全天成交量，自动标记[疑似ADR转换/实物存取仓]，防止误判外资砸盘或扫货。',
      },
    ],
  },
};

export function ProvenanceDrawer({ open, market, onClose }: ProvenanceDrawerProps) {
  if (!open) return null;
  const spec = METHODOLOGY[market];

  return (
    <div className="fixed inset-0 z-50 flex items-end sm:items-center justify-center bg-slate-950/50 backdrop-blur-xs p-0 sm:p-4">
      <div className="w-full sm:max-w-2xl max-h-[88vh] overflow-y-auto rounded-t-3xl sm:rounded-3xl bg-[var(--bg-surface)] border border-[var(--border-color)] shadow-2xl p-5 sm:p-6">
        <div className="flex items-start justify-between gap-3 pb-4 border-b border-[var(--border-color)]">
          <div>
            <div className="inline-flex items-center gap-1.5 text-xs font-semibold text-blue-600 dark:text-blue-400 mb-1">
              <ShieldCheck className="w-4 h-4" />
              数据溯源与计算口径审计
            </div>
            <h3 className="text-lg font-bold">{spec.title}</h3>
            <p className="text-xs text-[var(--text-secondary)] mt-0.5">计价币种：{spec.currency}</p>
          </div>
          <button
            onClick={onClose}
            className="p-2 rounded-full hover:bg-[var(--bg-subtle)] transition-colors"
            aria-label="关闭"
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        <div className="mt-4 space-y-4">
          <div>
            <h4 className="text-xs font-semibold uppercase tracking-wider text-[var(--text-secondary)] mb-2">
              权威官方信源
            </h4>
            <div className="space-y-2">
              {spec.sources.map((s) => (
                <a
                  key={s.name}
                  href={s.url}
                  target="_blank"
                  rel="noreferrer"
                  className="flex items-start justify-between gap-3 p-3 rounded-xl bg-[var(--bg-subtle)]/70 hover:bg-[var(--bg-subtle)] transition-colors"
                >
                  <div>
                    <div className="text-sm font-medium">{s.name}</div>
                    <div className="text-xs text-[var(--text-secondary)] mt-0.5">更新频率：{s.freq}</div>
                  </div>
                  <div className="flex items-center gap-1.5 shrink-0">
                    <span className="px-2 py-0.5 rounded-full text-[11px] font-medium bg-blue-500/10 text-blue-600 dark:text-blue-400">
                      {s.badge}
                    </span>
                    <ExternalLink className="w-3.5 h-3.5 text-[var(--text-secondary)]" />
                  </div>
                </a>
              ))}
            </div>
          </div>

          <div>
            <h4 className="text-xs font-semibold uppercase tracking-wider text-[var(--text-secondary)] mb-2 flex items-center gap-1.5">
              <Calculator className="w-3.5 h-3.5" />
              透明计算公式与边界说明
            </h4>
            <div className="space-y-2.5">
              {spec.formulas.map((f) => (
                <div key={f.label} className="p-3.5 rounded-xl border border-[var(--border-color)]">
                  <div className="text-sm font-semibold">{f.label}</div>
                  <code className="block mt-1.5 px-2.5 py-1.5 rounded-lg bg-[var(--bg-subtle)] text-xs font-mono text-blue-700 dark:text-blue-300 overflow-x-auto">
                    {f.formula}
                  </code>
                  <p className="text-xs text-[var(--text-secondary)] mt-2 leading-relaxed">{f.note}</p>
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
