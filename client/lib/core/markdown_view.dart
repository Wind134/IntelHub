import 'package:flutter/material.dart';
import 'package:flutter_markdown_plus/flutter_markdown_plus.dart';
import 'package:url_launcher/url_launcher.dart';

/// Theme-aware GFM Markdown renderer (headings, bold, lists, tables, blockquotes, links, selectable)
/// unified for AI follow-up threads, Historical RAG reports, and Daily Digests.
class IntelMarkdownView extends StatelessWidget {
  final String data;

  const IntelMarkdownView({super.key, required this.data});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final cs = theme.colorScheme;
    final tt = theme.textTheme;
    final isDark = theme.brightness == Brightness.dark;

    return MarkdownBody(
      data: data,
      selectable: true,
      softLineBreak: true,
      onTapLink: (_, href, __) {
        if (href != null && href.isNotEmpty) {
          final uri = Uri.tryParse(href);
          if (uri != null) {
            launchUrl(uri, mode: LaunchMode.externalApplication);
          }
        }
      },
      styleSheet: MarkdownStyleSheet(
        p: tt.bodyMedium?.copyWith(height: 1.65),
        pPadding: const EdgeInsets.only(bottom: 8),
        h1: tt.headlineSmall?.copyWith(fontSize: 18, fontWeight: FontWeight.w600),
        h1Padding: const EdgeInsets.only(top: 12, bottom: 6),
        h2: tt.titleLarge?.copyWith(fontSize: 16, fontWeight: FontWeight.w600),
        h2Padding: const EdgeInsets.only(top: 12, bottom: 6),
        h3: tt.titleMedium?.copyWith(fontSize: 14.5, fontWeight: FontWeight.w600),
        h3Padding: const EdgeInsets.only(top: 10, bottom: 4),
        strong: TextStyle(fontWeight: FontWeight.w600, color: cs.onSurface),
        em: TextStyle(fontStyle: FontStyle.italic, color: cs.onSurface),
        listBullet: tt.bodyMedium?.copyWith(color: cs.primary, fontWeight: FontWeight.w600),
        listIndent: 20,
        blockSpacing: 8,
        a: TextStyle(
          color: cs.primary,
          decoration: TextDecoration.underline,
          decorationColor: cs.primary.withValues(alpha: 0.5),
        ),
        blockquote: tt.bodyMedium?.copyWith(color: cs.onSurface.withValues(alpha: 0.85), height: 1.6),
        blockquotePadding: const EdgeInsets.fromLTRB(14, 10, 14, 10),
        blockquoteDecoration: BoxDecoration(
          color: cs.primary.withValues(alpha: isDark ? 0.1 : 0.05),
          borderRadius: const BorderRadius.only(
            topRight: Radius.circular(10),
            bottomRight: Radius.circular(10),
          ),
          border: Border(left: BorderSide(color: cs.primary, width: 3.5)),
        ),
        horizontalRuleDecoration: BoxDecoration(
          border: Border(top: BorderSide(color: cs.outline, width: 1)),
        ),
        code: TextStyle(
          fontFamily: 'Consolas',
          fontSize: 12.5,
          color: cs.primary,
          backgroundColor: cs.surfaceContainer,
        ),
        codeblockPadding: const EdgeInsets.all(12),
        codeblockDecoration: BoxDecoration(
          color: cs.surfaceContainerLowest,
          borderRadius: BorderRadius.circular(12),
          border: Border.all(color: cs.outline),
        ),
        tableHead: tt.labelLarge?.copyWith(fontWeight: FontWeight.w600),
        tableBody: tt.bodySmall?.copyWith(color: cs.onSurface),
        tableBorder: TableBorder.all(color: cs.outline, width: 0.8, borderRadius: BorderRadius.circular(8)),
        tableCellsPadding: const EdgeInsets.symmetric(horizontal: 10, vertical: 7),
        tableHeadAlign: TextAlign.left,
      ),
    );
  }
}
