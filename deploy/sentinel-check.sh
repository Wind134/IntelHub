#!/usr/bin/env bash
set -euo pipefail

[ -f "$HOME/.config/intel-sentinel/sentinel.env" ] && source "$HOME/.config/intel-sentinel/sentinel.env"
HEALTH_URL="${INTEL_HEALTH_URL:-https://intel.example.com/api/v1/health}"
FEISHU_WEBHOOK="${FEISHU_OPS_WEBHOOK:-}"
STATE_FILE="/tmp/intel_hub_sentinel_fails"
ALERTED_FILE="/tmp/intel_hub_sentinel_alerted"
LOG_FILE="$HOME/bin/sentinel.log"

TS=$(TZ=Asia/Shanghai date "+%Y-%m-%d %H:%M:%S")
if curl -fsS --max-time 10 "$HEALTH_URL" | grep -q '"ok":true'; then
  rm -f "$STATE_FILE"
  if [ -f "$ALERTED_FILE" ]; then
    rm -f "$ALERTED_FILE"
    "$HOME/bin/usserver-mail.py" alert \
      --subject "✅ [恢复通知] 情报中枢主节点已恢复正常" \
      --body "恢复时间：${TS} (UTC+8)\n探测地址：${HEALTH_URL}\n状态：HTTP 200 正常响应。" >/dev/null 2>&1 || true
  fi
  echo "[$TS] OK $HEALTH_URL" >> "$LOG_FILE"
  tail -n 200 "$LOG_FILE" > "${LOG_FILE}.tmp" && mv "${LOG_FILE}.tmp" "$LOG_FILE"
  exit 0
fi

FAILS=0
[ -f "$STATE_FILE" ] && FAILS=$(cat "$STATE_FILE")
FAILS=$((FAILS + 1))
echo "$FAILS" > "$STATE_FILE"
echo "[$TS] FAIL($FAILS) $HEALTH_URL" >> "$LOG_FILE"

if [ "$FAILS" -eq 2 ]; then
  touch "$ALERTED_FILE"
  "$HOME/bin/usserver-mail.py" alert \
    --subject "🔴 [故障告警] 情报中枢主节点连续失联" \
    --body "告警时间：${TS} (UTC+8)\n探测地址：${HEALTH_URL}\n故障现象：备机哨兵连续 2 次（10分钟）探测失败。\n请 SSH 登录主节点检查 intel-hub.service 与 cloudflared 状态。" >/dev/null 2>&1 || true

  if [ -n "$FEISHU_WEBHOOK" ]; then
    curl -fsS -X POST "$FEISHU_WEBHOOK" \
      -H "Content-Type: application/json" \
      -d "{\"msg_type\":\"text\",\"content\":{\"text\":\"[运维告警] 情报中枢健康探针连续失败 (${HEALTH_URL})，请检查 intel-hub 或 cloudflared 状态。\"}}" >/dev/null || true
  fi
fi
