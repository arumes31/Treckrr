#!/bin/sh
# One-off manual database backup into ./backups (custom pg_dump format).
# Usage: sh scripts/backup.sh
set -e
cd "$(dirname "$0")/.."
mkdir -p backups
ts=$(date +%Y%m%d-%H%M%S)
out="backups/treckrr-$ts.dump"
docker compose exec -T db pg_dump -U "${POSTGRES_USER:-treckrr}" -d "${POSTGRES_DB:-treckrr}" -F c > "$out"
echo "Backup written to $out"
