#!/bin/bash
set -euo pipefail

BACKUP_DIR="/opt/backups"
DATE=$(date +%Y-%m-%d_%H-%M-%S)
RETENTION_DAYS=5

mkdir -p "$BACKUP_DIR/postgres" "$BACKUP_DIR/volumes"

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"; }

pg_running() { docker ps --format '{{.Names}}' | grep -qx "$1"; }
vol_exists() { docker volume ls --format '{{.Name}}' | grep -qx "$1"; }

# --- PostgreSQL dumps (guarded: skip if container not present) ---
if pg_running authelia-postgres; then
  log "Dumping authelia postgres..."
  docker exec authelia-postgres pg_dump -U authelia authelia | gzip > "$BACKUP_DIR/postgres/authelia_$DATE.sql.gz"
else
  log "skip: authelia-postgres not running"
fi

if pg_running affine_postgres; then
  log "Dumping affine postgres..."
  docker exec affine_postgres pg_dump -U affine affine | gzip > "$BACKUP_DIR/postgres/affine_$DATE.sql.gz"
else
  log "skip: affine_postgres not running (AFFiNE lives on bunker)"
fi

if pg_running teamspeak_postgres; then
  log "Dumping teamspeak postgres..."
  docker exec teamspeak_postgres pg_dump -U teamspeak teamspeak | gzip > "$BACKUP_DIR/postgres/teamspeak_$DATE.sql.gz"
else
  log "skip: teamspeak_postgres not running"
fi

# --- Volume backups (guarded: skip if volume not present) ---
backup_volume() { # $1 = volume name, $2 = label
  if vol_exists "$1"; then
    log "Backing up $1..."
    docker run --rm -v "$1:/data:ro" -v "$BACKUP_DIR/volumes:/backups" alpine tar czf "/backups/$2_$DATE.tar.gz" -C /data .
  else
    log "skip volume: $1 (not present)"
  fi
}

backup_volume bar-assistant_bar_data bar_data
backup_volume bar-assistant_meilisearch_data meilisearch
backup_volume open-webui_open-webui-data open_webui

log "Backing up affine storage (if present)..."
tar czf "$BACKUP_DIR/volumes/affine_storage_$DATE.tar.gz" -C ~/.affine/self-host storage config 2>/dev/null \
  || log "Warning: affine storage backup skipped (may not exist)"

# --- Cleanup (scoped to this script's own subdirs) ---
log "Removing backups older than $RETENTION_DAYS days..."
find "$BACKUP_DIR/postgres" "$BACKUP_DIR/volumes" -type f -name "*.gz" -mtime +$RETENTION_DAYS -delete 2>/dev/null || true

log "Backup complete. Current files:"
find "$BACKUP_DIR/postgres" "$BACKUP_DIR/volumes" -type f -name "*.gz" 2>/dev/null | sort
