#!/bin/sh
# Restore the database from a custom-format dump created by backup.sh.
# Usage: sh scripts/restore.sh backups/treckrr-YYYYmmdd-HHMMSS.dump
# WARNING: this overwrites current data (drops & recreates objects).
set -e
cd "$(dirname "$0")/.."
dump="$1"
if [ -z "$dump" ] || [ ! -f "$dump" ]; then
  echo "usage: sh scripts/restore.sh <dumpfile>"
  exit 1
fi
echo "Restoring $dump into database '${POSTGRES_DB:-treckrr}' ..."
docker compose exec -T db pg_restore -U "${POSTGRES_USER:-treckrr}" -d "${POSTGRES_DB:-treckrr}" \
  --clean --if-exists --no-owner < "$dump"
echo "Restore complete."
