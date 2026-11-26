#!/usr/bin/env bash
set -euo pipefail

# Optional local override file (~/.config/intel-sentinel/backup.env):
#   BACKUP_REMOTE_TARGET="user@backup-host"
#   BACKUP_REMOTE_PORT="22"
#   BACKUP_SSH_KEY="$HOME/.ssh/id_intel_backup"
[ -f "$HOME/.config/intel-sentinel/backup.env" ] && source "$HOME/.config/intel-sentinel/backup.env"

BACKUP_DIR="$HOME/.hermes/intel/backups"
REMOTE_TARGET="${BACKUP_REMOTE_TARGET:?Set BACKUP_REMOTE_TARGET in ~/.config/intel-sentinel/backup.env}"
REMOTE_PORT="${BACKUP_REMOTE_PORT:-22}"
SSH_KEY="${BACKUP_SSH_KEY:-$HOME/.ssh/id_intel_backup}"

mkdir -p "$BACKUP_DIR"
STAMP=$(TZ=Asia/Shanghai date "+%Y%m%d_%H%M%S")
SNAP="$BACKUP_DIR/intel_hub_${STAMP}.db"

# Create consistent hot snapshot via SQLite VACUUM INTO
"$HOME/.local/bin/intel-hub" backup --out "$SNAP" >/dev/null
chmod 600 "$SNAP"

# Sync snapshot to backup server over dedicated SSH key
scp -q -o BatchMode=yes -i "$SSH_KEY" -P "$REMOTE_PORT" \
  "$SNAP" "${REMOTE_TARGET}:~/backups/intel-hub/"

# Keep last 3 days locally, last 14 days on backup server
find "$BACKUP_DIR" -name 'intel_hub_*.db' -mtime +3 -delete
ssh -o BatchMode=yes -i "$SSH_KEY" -p "$REMOTE_PORT" "$REMOTE_TARGET" \
  "find ~/backups/intel-hub -name 'intel_hub_*.db' -mtime +14 -delete"

TS=$(TZ=Asia/Shanghai date "+%Y-%m-%d %H:%M:%S")
echo "[$TS] BACKUP_OK $(basename "$SNAP")" >> "$HOME/bin/backup.log"
