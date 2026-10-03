#!/usr/bin/env bash
# 容器入口: 等待依赖 -> (可选)种子 -> 启动服务
# 迁移在应用启动时自动执行(migrations/*.sql, IF NOT EXISTS 幂等)
set -euo pipefail

: "${PG_DSN:?PG_DSN required}"

# 解析 PG host/port(支持 postgres:// DSN, 缺省 db:5432)
PG_HOST="${PG_HOST:-db}"
PG_PORT="${PG_PORT:-5432}"

echo "==> waiting for postgres ${PG_HOST}:${PG_PORT}"
for i in $(seq 1 60); do
  if pg_isready -h "$PG_HOST" -p "$PG_PORT" -U postgres >/dev/null 2>&1; then break; fi
  sleep 1
done

if [ "${RUN_SEED:-1}" = "1" ]; then
  echo "==> seeding (idempotent)"
  /app/recycle-app seed || echo "seed skipped/failed (may already be seeded)"
fi

echo "==> starting recycle-app (${APP_MODE:-all})"
exec /app/recycle-app "${APP_MODE:-all}"
