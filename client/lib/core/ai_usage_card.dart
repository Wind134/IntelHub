import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import 'models.dart';

final DateFormat _fmtResetShort = DateFormat('MM-dd HH:mm');
final DateFormat _fmtTimeOnly = DateFormat('HH:mm');

/// Displays OpenCode Go live subscription quota alongside IntelHub's local token ledger.
class AiUsageCard extends StatelessWidget {
  final LlmUsageSummary? usage;
  final bool refreshing;
  final VoidCallback onRefresh;

  const AiUsageCard({
    super.key,
    required this.usage,
    required this.refreshing,
    required this.onRefresh,
  });

  @override
  Widget build(BuildContext context) {
    final cs = Theme.of(context).colorScheme;
    final tt = Theme.of(context).textTheme;
    final u = usage;
    final quota = u?.quota;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Expanded(child: Text('AI 订阅额度与 Token 消耗', style: tt.titleLarge)),
            if (quota != null && quota.available)
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
                margin: const EdgeInsets.only(right: 4),
                decoration: BoxDecoration(
                  color: const Color(0xFF10B981).withValues(alpha: 0.12),
                  borderRadius: BorderRadius.circular(999),
                ),
                child: Text(
                  '${quota.provider} 实时同步',
                  style: const TextStyle(fontSize: 11, fontWeight: FontWeight.w600, color: Color(0xFF059669)),
                ),
              ),
            IconButton(
              tooltip: '立即刷新订阅额度与 Token 统计',
              onPressed: refreshing ? null : onRefresh,
              icon: refreshing
                  ? const SizedBox(width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2))
                  : const Icon(Icons.refresh_rounded, size: 18),
            ),
          ],
        ),
        const SizedBox(height: 4),
        Text(
          '上方展示 OpenCode Go 官方实时剩余额度（全账号口径）；下方统计本情报系统各场景与模型的精确 Token 消耗。',
          style: tt.bodySmall,
        ),
        const SizedBox(height: 10),
        Card(
          child: Padding(
            padding: const EdgeInsets.all(18),
            child: u == null
                ? Center(
                    child: Padding(
                      padding: const EdgeInsets.symmetric(vertical: 14),
                      child: Text('连接服务端后将自动加载额度与 Token 账本', style: tt.bodySmall),
                    ),
                  )
                : LayoutBuilder(
                    builder: (context, constraints) {
                      final wide = constraints.maxWidth >= 620;
                      return Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          if (quota != null) ...[
                            _buildQuotaSection(context, cs, tt, quota, wide),
                            const Divider(height: 28),
                          ],
                          _buildLocalLedgerSection(context, cs, tt, u, wide),
                        ],
                      );
                    },
                  ),
          ),
        ),
      ],
    );
  }

  Widget _buildQuotaSection(
    BuildContext context,
    ColorScheme cs,
    TextTheme tt,
    SubscriptionQuota quota,
    bool wide,
  ) {
    final windows = <(String, QuotaWindow)>[
      if (quota.rolling != null) ('5 小时滚动窗口', quota.rolling!),
      if (quota.weekly != null) ('每周额度', quota.weekly!),
      if (quota.monthly != null) ('每月额度', quota.monthly!),
    ];

    final fetchedStr = quota.fetchedAt > 0
        ? _fmtTimeOnly.format(DateTime.fromMillisecondsSinceEpoch(quota.fetchedAt * 1000))
        : '';

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Icon(Icons.bolt_rounded, size: 17, color: cs.primary),
            const SizedBox(width: 6),
            Text('OpenCode Go 订阅剩余额度', style: tt.titleSmall),
            const Spacer(),
            if (fetchedStr.isNotEmpty)
              Text('更新于 $fetchedStr', style: tt.bodySmall?.copyWith(fontSize: 11)),
          ],
        ),
        if (quota.error.isNotEmpty) ...[
          const SizedBox(height: 8),
          Container(
            width: double.infinity,
            padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 7),
            decoration: BoxDecoration(
              color: const Color(0xFFF59E0B).withValues(alpha: 0.12),
              borderRadius: BorderRadius.circular(10),
            ),
            child: Text(
              quota.available ? '实时刷新受阻（展示近期缓存）：${quota.error}' : '暂未获取到官方额度：${quota.error}',
              style: const TextStyle(fontSize: 12, color: Color(0xFFB45309)),
            ),
          ),
        ],
        if (quota.available && windows.isNotEmpty) ...[
          const SizedBox(height: 12),
          if (wide)
            Row(
              children: [
                for (var i = 0; i < windows.length; i++) ...[
                  if (i > 0) const SizedBox(width: 12),
                  Expanded(child: _QuotaWindowTile(title: windows[i].$1, window: windows[i].$2)),
                ],
              ],
            )
          else
            Column(
              children: [
                for (var i = 0; i < windows.length; i++) ...[
                  if (i > 0) const SizedBox(height: 10),
                  _QuotaWindowTile(title: windows[i].$1, window: windows[i].$2),
                ],
              ],
            ),
        ],
      ],
    );
  }

  Widget _buildLocalLedgerSection(
    BuildContext context,
    ColorScheme cs,
    TextTheme tt,
    LlmUsageSummary u,
    bool wide,
  ) {
    final total7d = u.daily7d.fold<int>(0, (sum, d) => sum + d.totalTokens);
    final activeDays = u.daily7d.where((d) => d.totalTokens > 0).length;
    final avg7d = activeDays > 0 ? (total7d / activeDays).round() : 0;
    final hasEst = u.month.estimatedCount > 0;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Icon(Icons.analytics_outlined, size: 17, color: cs.primary),
            const SizedBox(width: 6),
            Text('IntelHub 本地 Token 账本', style: tt.titleSmall),
            const Spacer(),
            Text(u.month.key, style: tt.bodySmall?.copyWith(fontSize: 11)),
          ],
        ),
        const SizedBox(height: 12),
        Wrap(
          spacing: 12,
          runSpacing: 12,
          children: [
            _TokenKpiBox(
              label: '今日消耗',
              primary: formatTokenCount(u.today.totalTokens),
              detail: '入 ${formatTokenCount(u.today.promptTokens)} · 出 ${formatTokenCount(u.today.completionTokens)} · ${u.today.requestCount} 次',
              accent: cs.primary,
            ),
            _TokenKpiBox(
              label: '本月累计',
              primary: formatTokenCount(u.month.totalTokens),
              detail: '入 ${formatTokenCount(u.month.promptTokens)} · 出 ${formatTokenCount(u.month.completionTokens)} · ${u.month.requestCount} 次',
              accent: const Color(0xFF10B981),
            ),
            _TokenKpiBox(
              label: '近 7 日活跃日均',
              primary: formatTokenCount(avg7d),
              detail: '近 7 日合计 ${formatTokenCount(total7d)}',
              accent: const Color(0xFF6366F1),
            ),
          ],
        ),
        if (u.daily7d.isNotEmpty) ...[
          const SizedBox(height: 16),
          Text('近 7 日消耗趋势', style: tt.labelLarge),
          const SizedBox(height: 8),
          _Mini7dBarChart(days: u.daily7d),
        ],
        if (u.byScene.isNotEmpty || u.byModel.isNotEmpty) ...[
          const SizedBox(height: 18),
          if (wide)
            Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Expanded(child: _BreakdownList(title: '本月按场景拆解', items: u.byScene, barColor: cs.primary)),
                const SizedBox(width: 20),
                Expanded(child: _BreakdownList(title: '本月按模型拆解', items: u.byModel, barColor: const Color(0xFF10B981))),
              ],
            )
          else ...[
            _BreakdownList(title: '本月按场景拆解', items: u.byScene, barColor: cs.primary),
            const SizedBox(height: 16),
            _BreakdownList(title: '本月按模型拆解', items: u.byModel, barColor: const Color(0xFF10B981)),
          ],
        ],
        if (hasEst) ...[
          const SizedBox(height: 10),
          Text(
            '* 本月含 ${u.month.estimatedCount} 次流式无回传 usage 的本地估算值，订阅总余量以上方官方数据为准。',
            style: tt.bodySmall?.copyWith(fontSize: 11),
          ),
        ],
      ],
    );
  }
}

class _QuotaWindowTile extends StatelessWidget {
  final String title;
  final QuotaWindow window;

  const _QuotaWindowTile({required this.title, required this.window});

  @override
  Widget build(BuildContext context) {
    final cs = Theme.of(context).colorScheme;
    final rem = window.remainingPercent.clamp(0.0, 100.0);
    final used = window.usedPercent.clamp(0.0, 100.0);
    final color = rem >= 50
        ? const Color(0xFF10B981)
        : rem >= 20
            ? const Color(0xFFF59E0B)
            : cs.error;

    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: cs.surfaceContainerHighest.withValues(alpha: 0.45),
        borderRadius: BorderRadius.circular(14),
        border: Border.all(color: cs.outline.withValues(alpha: 0.45)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(title, style: Theme.of(context).textTheme.bodySmall?.copyWith(fontWeight: FontWeight.w600)),
              ),
              Text(
                '剩余 ${_fmtPct(rem)}',
                style: TextStyle(fontSize: 15, fontWeight: FontWeight.w700, color: color),
              ),
            ],
          ),
          const SizedBox(height: 8),
          ClipRRect(
            borderRadius: BorderRadius.circular(999),
            child: LinearProgressIndicator(
              value: rem / 100.0,
              minHeight: 7,
              backgroundColor: cs.outline.withValues(alpha: 0.25),
              color: color,
            ),
          ),
          const SizedBox(height: 7),
          Row(
            children: [
              Text('已用 ${_fmtPct(used)}', style: Theme.of(context).textTheme.bodySmall?.copyWith(fontSize: 11)),
              const Spacer(),
              Flexible(
                child: Text(
                  formatResetCountdown(window.resetsAt),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: Theme.of(context).textTheme.bodySmall?.copyWith(fontSize: 11),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _TokenKpiBox extends StatelessWidget {
  final String label;
  final String primary;
  final String detail;
  final Color accent;

  const _TokenKpiBox({
    required this.label,
    required this.primary,
    required this.detail,
    required this.accent,
  });

  @override
  Widget build(BuildContext context) {
    final cs = Theme.of(context).colorScheme;
    return Container(
      constraints: const BoxConstraints(minWidth: 165),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color: accent.withValues(alpha: 0.07),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: accent.withValues(alpha: 0.22)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(label, style: Theme.of(context).textTheme.bodySmall?.copyWith(fontSize: 11)),
          const SizedBox(height: 3),
          Text(
            '$primary tokens',
            style: Theme.of(context).textTheme.titleMedium?.copyWith(fontWeight: FontWeight.w700, color: cs.onSurface),
          ),
          const SizedBox(height: 2),
          Text(detail, style: Theme.of(context).textTheme.bodySmall?.copyWith(fontSize: 11)),
        ],
      ),
    );
  }
}

class _Mini7dBarChart extends StatelessWidget {
  final List<LlmUsageBucket> days;

  const _Mini7dBarChart({required this.days});

  @override
  Widget build(BuildContext context) {
    final cs = Theme.of(context).colorScheme;
    final maxTok = days.fold<int>(0, (m, d) => d.totalTokens > m ? d.totalTokens : m);
    return Container(
      padding: const EdgeInsets.fromLTRB(10, 12, 10, 8),
      decoration: BoxDecoration(
        color: cs.surfaceContainerHighest.withValues(alpha: 0.35),
        borderRadius: BorderRadius.circular(12),
      ),
      child: SizedBox(
        height: 82,
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.end,
          children: [
            for (var i = 0; i < days.length; i++)
              Expanded(
                child: Tooltip(
                  message: '${days[i].key}\n总计 ${formatTokenCount(days[i].totalTokens)} (${days[i].requestCount} 次)\n入 ${formatTokenCount(days[i].promptTokens)} / 出 ${formatTokenCount(days[i].completionTokens)}',
                  child: Column(
                    mainAxisAlignment: MainAxisAlignment.end,
                    children: [
                      Text(
                        days[i].totalTokens > 0 ? formatTokenCount(days[i].totalTokens) : '-',
                        style: Theme.of(context).textTheme.bodySmall?.copyWith(fontSize: 10),
                      ),
                      const SizedBox(height: 4),
                      AnimatedContainer(
                        duration: const Duration(milliseconds: 240),
                        curve: Curves.easeOutCubic,
                        width: 18,
                        height: maxTok > 0
                            ? (days[i].totalTokens / maxTok * 38).clamp(days[i].totalTokens > 0 ? 4.0 : 2.0, 38.0)
                            : 2.0,
                        decoration: BoxDecoration(
                          color: i == days.length - 1
                              ? cs.primary
                              : cs.primary.withValues(alpha: days[i].totalTokens > 0 ? 0.55 : 0.15),
                          borderRadius: BorderRadius.circular(5),
                        ),
                      ),
                      const SizedBox(height: 5),
                      Text(
                        days[i].label,
                        style: Theme.of(context).textTheme.bodySmall?.copyWith(fontSize: 10),
                      ),
                    ],
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }
}

class _BreakdownList extends StatelessWidget {
  final String title;
  final List<LlmUsageBucket> items;
  final Color barColor;

  const _BreakdownList({required this.title, required this.items, required this.barColor});

  @override
  Widget build(BuildContext context) {
    final cs = Theme.of(context).colorScheme;
    final tt = Theme.of(context).textTheme;
    final total = items.fold<int>(0, (s, e) => s + e.totalTokens);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(title, style: tt.labelLarge),
        const SizedBox(height: 8),
        if (items.isEmpty)
          Text('本月暂无调用记录', style: tt.bodySmall)
        else
          for (final item in items.take(6)) ...[
            Row(
              children: [
                Expanded(
                  child: Text(
                    item.label,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: tt.bodySmall?.copyWith(color: cs.onSurface, fontWeight: FontWeight.w500),
                  ),
                ),
                const SizedBox(width: 8),
                Text(
                  '${formatTokenCount(item.totalTokens)} · ${item.requestCount}次',
                  style: tt.bodySmall?.copyWith(fontSize: 11),
                ),
              ],
            ),
            const SizedBox(height: 4),
            ClipRRect(
              borderRadius: BorderRadius.circular(999),
              child: LinearProgressIndicator(
                value: total > 0 ? (item.totalTokens / total).clamp(0.02, 1.0) : 0,
                minHeight: 5,
                backgroundColor: cs.outline.withValues(alpha: 0.2),
                color: barColor,
              ),
            ),
            const SizedBox(height: 9),
          ],
      ],
    );
  }
}

String formatTokenCount(int n) {
  if (n >= 1000000) return '${(n / 1000000).toStringAsFixed(2)}M';
  if (n >= 10000) return '${(n / 1000).toStringAsFixed(1)}k';
  if (n >= 1000) return '${(n / 1000).toStringAsFixed(2)}k';
  return '$n';
}

String _fmtPct(double v) {
  final rounded = (v * 10).round() / 10;
  return rounded == rounded.roundToDouble() ? '${rounded.toInt()}%' : '${rounded.toStringAsFixed(1)}%';
}

String formatResetCountdown(String iso) {
  if (iso.isEmpty) return '自动滚动重置';
  final dt = DateTime.tryParse(iso)?.toLocal();
  if (dt == null) return '重置：$iso';
  final diff = dt.difference(DateTime.now());
  final timeLabel = _fmtResetShort.format(dt);
  if (diff.isNegative) return '即将重置 ($timeLabel)';
  if (diff.inDays >= 1) {
    return '${diff.inDays}天${diff.inHours % 24}时后重置 ($timeLabel)';
  }
  if (diff.inHours >= 1) {
    return '${diff.inHours}时${diff.inMinutes % 60}分后重置 (${_fmtTimeOnly.format(dt)})';
  }
  return '${diff.inMinutes.clamp(1, 59)}分后重置 (${_fmtTimeOnly.format(dt)})';
}
