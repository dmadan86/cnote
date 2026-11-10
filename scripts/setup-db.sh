#!/usr/bin/env bash
# Create the database if needed, run migrations, and set the app role password from DB_APP_PASSWORD.
# Usage: scripts/setup-db.sh   (reads .env when present)
set -euo pipefail
cd "$(dirname "$0")/.."
[ -f .env ] && set -a && . ./.env && set +a
: "${DATABASE_ADMIN_URL:?DATABASE_ADMIN_URL is required}"
: "${DB_APP_PASSWORD:?DB_APP_PASSWORD is required}"
DB_NAME="${DATABASE_ADMIN_URL##*/}"; DB_NAME="${DB_NAME%%\?*}"
psql "${DATABASE_ADMIN_URL%/*}/postgres" -tAc "select 1 from pg_database where datname='${DB_NAME}'" | grep -q 1 \
  || psql "${DATABASE_ADMIN_URL%/*}/postgres" -c "create database \"${DB_NAME}\""
cargo run -q -p cnote-server -- migrate
echo "alter role cnote_app password :'pw';" | psql "$DATABASE_ADMIN_URL" -q -v ON_ERROR_STOP=1 -v pw="$DB_APP_PASSWORD" >/dev/null
echo "database ready"
