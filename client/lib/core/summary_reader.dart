import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

/// Zero-cost editorial reader for news summaries:
/// - Preserves real paragraph breaks from newer server records
/// - Automatically splits legacy single-block "walls of text" at natural Chinese/English sentence boundaries
/// - Renders in a clean card with comfortable line-height, paragraph spacing, and one-tap copy.
class IntelSummaryReader extends StatefulWidget {
  final String summary;
  final String sourceName;

  const IntelSummaryReader({
    super.key,
    required this.summary,
    required this.sourceName,
  });

  @override
  State<IntelSummaryReader> createState() => _IntelSummaryReaderState();
}

class _IntelSummaryReaderState extends State<IntelSummaryReader> {
  bool _expanded = false;
  bool _copied = false;

  @override
  void didUpdateWidget(covariant IntelSummaryReader oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.summary != widget.summary) {
      _expanded = false;
      _copied = false;
    }
  }

  Future<void> _copySummary() async {
    await Clipboard.setData(ClipboardData(text: widget.summary));
    if (!mounted) return;
    setState(() => _copied = true);
    Future.delayed(const Duration(seconds: 2), () {
      if (mounted) setState(() => _copied = false);
    });
  }

  @override
  Widget build(BuildContext context) {
    final text = widget.summary.trim();
    if (text.isEmpty) return const SizedBox.shrink();

    final cs = Theme.of(context).colorScheme;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final paragraphs = splitIntoReadableParagraphs(text);
    final isLong = paragraphs.length > 5 && text.length > 520;
    final visible = (isLong && !_expanded) ? paragraphs.take(4).toList() : paragraphs;

    final bodyStyle = Theme.of(context).textTheme.bodyLarge?.copyWith(
          height: 1.76,
          letterSpacing: 0.15,
          color: cs.onSurface.withValues(alpha: isDark ? 0.92 : 0.88),
        );

    return Container(
      width: double.infinity,
      padding: const EdgeInsets.fromLTRB(18, 14, 18, 16),
      decoration: BoxDecoration(
        color: cs.surface,
        borderRadius: BorderRadius.circular(18),
        border: Border.all(color: cs.outline, width: 1),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(Icons.article_outlined, size: 15, color: cs.onSurface.withValues(alpha: 0.55)),
              const SizedBox(width: 6),
              Text(
                '正文摘要',
                style: TextStyle(
                  fontSize: 12.5,
                  fontWeight: FontWeight.w600,
                  color: cs.onSurface.withValues(alpha: 0.65),
                ),
              ),
              const SizedBox(width: 8),
              Text(
                '${paragraphs.length} 段 · ${text.length} 字',
                style: Theme.of(context).textTheme.labelSmall,
              ),
              const Spacer(),
              InkWell(
                borderRadius: BorderRadius.circular(8),
                onTap: _copySummary,
                child: Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 3),
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Icon(
                        _copied ? Icons.check_rounded : Icons.copy_rounded,
                        size: 13.5,
                        color: _copied ? const Color(0xFF10B981) : cs.onSurface.withValues(alpha: 0.55),
                      ),
                      const SizedBox(width: 4),
                      Text(
                        _copied ? '已复制' : '复制',
                        style: TextStyle(
                          fontSize: 11.5,
                          color: _copied ? const Color(0xFF10B981) : cs.onSurface.withValues(alpha: 0.6),
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ],
          ),
          Divider(height: 18, color: cs.outline.withValues(alpha: 0.7)),
          SelectionArea(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                for (int i = 0; i < visible.length; i++) ...[
                  if (i == 0 && visible.length > 1)
                    Container(
                      padding: const EdgeInsets.only(left: 11),
                      decoration: BoxDecoration(
                        border: Border(
                          left: BorderSide(color: cs.primary.withValues(alpha: 0.45), width: 2.5),
                        ),
                      ),
                      child: Text(
                        visible[i],
                        style: bodyStyle?.copyWith(fontWeight: FontWeight.w500),
                      ),
                    )
                  else
                    Text(visible[i], style: bodyStyle),
                  if (i != visible.length - 1) const SizedBox(height: 12),
                ],
              ],
            ),
          ),
          if (isLong) ...[
            const SizedBox(height: 10),
            Align(
              alignment: Alignment.centerLeft,
              child: TextButton.icon(
                style: TextButton.styleFrom(
                  padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
                  visualDensity: VisualDensity.compact,
                ),
                onPressed: () => setState(() => _expanded = !_expanded),
                icon: Icon(_expanded ? Icons.expand_less_rounded : Icons.expand_more_rounded, size: 18),
                label: Text(_expanded ? '收起正文' : '展开全部 ${paragraphs.length} 段'),
              ),
            ),
          ],
        ],
      ),
    );
  }
}

/// Splits raw or already-flattened article summaries into comfortable reading paragraphs
/// without any LLM calls: preserves existing newlines and smartly breaks long blocks
/// every ~115-160 characters on real sentence boundaries (protecting decimals/quotes).
List<String> splitIntoReadableParagraphs(String raw) {
  final normalized = raw.replaceAll('\r\n', '\n').replaceAll('\r', '\n').trim();
  if (normalized.isEmpty) return const [];

  final rawParas = normalized
      .split(RegExp(r'\n+'))
      .map((e) => e.trim())
      .where((e) => e.isNotEmpty)
      .toList();

  final result = <String>[];
  for (final para in rawParas) {
    if (para.length <= 165) {
      result.add(para);
    } else {
      result.addAll(_splitLongParagraph(para));
    }
  }
  return result;
}

List<String> _splitLongParagraph(String text) {
  final sentences = <String>[];
  final buf = StringBuffer();
  final len = text.length;

  bool isDigit(int codeUnit) => codeUnit >= 0x30 && codeUnit <= 0x39;
  bool isClosingQuote(String ch) => ch == '”' || ch == '’' || ch == '」' || ch == '』' || ch == ')' || ch == '）' || ch == '"';

  for (int i = 0; i < len; i++) {
    final ch = text[i];
    buf.write(ch);

    final isZhEnd = ch == '。' || ch == '！' || ch == '？';
    var isEnEnd = false;
    if (ch == '.' || ch == '!' || ch == '?') {
      final nextIsSpaceOrEnd = (i + 1 >= len) || text[i + 1] == ' ';
      final prevIsDigit = i > 0 && isDigit(text.codeUnitAt(i - 1));
      final nextIsDigit = i + 1 < len && isDigit(text.codeUnitAt(i + 1));
      // Avoid breaking numbers like 47.63% or abbreviations like U.S.
      final prevTwoDot = i >= 2 && text[i - 2] == '.';
      if (nextIsSpaceOrEnd && !(prevIsDigit && nextIsDigit) && !prevTwoDot) {
        isEnEnd = true;
      }
    }

    if (isZhEnd || isEnEnd) {
      while (i + 1 < len && isClosingQuote(text[i + 1])) {
        i++;
        buf.write(text[i]);
      }
      final s = buf.toString().trim();
      if (s.isNotEmpty) sentences.add(s);
      buf.clear();
    }
  }
  final tail = buf.toString().trim();
  if (tail.isNotEmpty) sentences.add(tail);

  if (sentences.length <= 1) return [text];

  final out = <String>[];
  final cur = StringBuffer();
  for (final s in sentences) {
    if (cur.isNotEmpty) {
      // Keep space between English sentences, none needed for CJK
      final lastChar = cur.toString().codeUnitAt(cur.length - 1);
      if (lastChar < 128 && s.codeUnitAt(0) < 128) {
        cur.write(' ');
      }
    }
    cur.write(s);
    if (cur.length >= 118) {
      out.add(cur.toString().trim());
      cur.clear();
    }
  }
  if (cur.isNotEmpty) {
    final rem = cur.toString().trim();
    if (out.isNotEmpty && rem.length < 40) {
      out[out.length - 1] = '${out.last}$rem';
    } else {
      out.add(rem);
    }
  }
  return out;
}
