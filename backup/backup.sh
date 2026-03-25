#!/bin/bash
set -euo pipefail

BACKUP_DIR="/opt/backups"
DATE=$(date +%Y-%m-%d_%H-%M-%S)
RETENTION_DAYS=5

mkdir -p "$BACKUP_DIR/postgres" "$BACKUP_DIR/volumes"

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"; }

# --- PostgreSQL dumps ---
log "Dumping authelia postgres..."
docker exec authelia-postgres pg_dump -U authelia authelia | gzip > "$BACKUP_DIR/postgres/authelia_$DATE.sql.gz"

log "Dumping affine postgres..."
docker exec affine_postgres pg_dump -U affine affine | gzip > "$BACKUP_DIR/postgres/affine_$DATE.sql.gz"

log "Dumping teamspeak postgres..."
docker exec teamspeak_postgres pg_dump -U teamspeak teamspeak | gzip > "$BACKUP_DIR/postgres/teamspeak_$DATE.sql.gz"

# --- Volume backups ---
log "Backing up bar-assistant data..."
docker run --rm \
  -v bar-assistant_bar_data:/data:ro \
  -v "$BACKUP_DIR/volumes:/backups" \
  alpine tar czf "/backups/bar_data_$DATE.tar.gz" -C /data .

log "Backing up meilisearch data..."
docker run --rm \
  -v bar-assistant_meilisearch_data:/data:ro \
  -v "$BACKUP_DIR/volumes:/backups" \
  alpine tar czf "/backups/meilisearch_$DATE.tar.gz" -C /data .

log "Backing up open-webui data..."
docker run --rm \
  -v open-webui_open-webui-data:/data:ro \
  -v "$BACKUP_DIR/volumes:/backups" \
  alpine tar czf "/backups/open_webui_$DATE.tar.gz" -C /data .

log "Backing up affine storage..."
tar czf "$BACKUP_DIR/volumes/affine_storage_$DATE.tar.gz" -C ~/.affine/self-host storage config 2>/dev/null \
  || log "Warning: affine storage backup failed (may not exist yet)"

# --- Cleanup ---
log "Removing backups older than $RETENTION_DAYS days..."
find "$BACKUP_DIR" -type f -name "*.gz" -mtime +$RETENTION_DAYS -delete

log "Backup complete. Current files:"
find "$BACKUP_DIR" -type f -name "*.gz" | sort
