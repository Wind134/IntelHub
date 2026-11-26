---
name: intel-hub
description: "Query real-time breaking alerts, full historical intelligence archive, entity timelines, and publish daily executive digests via local intel-hub CLI."
version: 1.0.0
metadata:
  hermes:
    tags: [Intel, BreakingNews, SQLite, Finance, AI, Digest]
---

# IntelHub Intelligence Bridge

Use `$HOME/.local/bin/intel-hub query ...` whenever the user asks about:
- A breaking alert they just received (e.g. `#EVT-20261125-0042` or "刚才那条英伟达突发")
- Historical events for any company, stock ticker, AI lab, or topic across the permanent SQLite archive
- Compiling the daily 21:00 executive digest (`《今日情报内参》`)

## Commands (zero external dependencies)

1. **Inspect a specific alert by code**:
   ```bash
   ~/.local/bin/intel-hub query event EVT-20261125-0042
   ```
2. **List recent high-signal alerts (last N hours)**:
   ```bash
   ~/.local/bin/intel-hub query recent --hours 12 --min-score 6 --limit 20
   ```
3. **Full historical search (Chinese + English FTS5)**:
   ```bash
   ~/.local/bin/intel-hub query search "台积电 OR NVDA" --days 0 --limit 25
   ```
4. **Entity historical evolution timeline**:
   ```bash
   ~/.local/bin/intel-hub query entity NVDA --limit 30
   ```
5. **Daily 21:00 Executive Digest workflow**:
   - Step A: Fetch pending 5-7 score items:
     ```bash
     ~/.local/bin/intel-hub query digest-pending
     ```
   - Step B: Synthesize into structured Chinese Markdown grouped by「金融市场主线 / AI 技术演进 / 大厂商业博弈 / 全球风险预警」(skip silently if empty).
   - Step C: Persist into `daily_digests` (so Windows & Android clients also display it) and acknowledge queue:
     ```bash
     cat <<'EOF' | ~/.local/bin/intel-hub query save-digest --title "今日情报内参"
     <generated markdown>
     EOF
     ```
6. **Check system health & $3/month API budget**:
   ```bash
   ~/.local/bin/intel-hub query stats
   ~/.local/bin/intel-hub budget
   ```
