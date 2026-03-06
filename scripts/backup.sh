#!/bin/sh
# 数据库备份守护脚本：
# 1) 按固定间隔执行 pg_dump
# 2) 失败时清理残缺文件
# 3) 按保留天数删除旧备份
set -eu

# 支持通过环境变量覆盖默认值，便于 docker-compose 注入。
: "${POSTGRES_HOST:=db}"
: "${POSTGRES_PORT:=5432}"
: "${POSTGRES_DB:=ai_gf}"
: "${POSTGRES_USER:=postgres}"
: "${BACKUP_DIR:=/backups}"
: "${BACKUP_INTERVAL_SECONDS:=21600}"
: "${BACKUP_RETENTION_DAYS:=7}"

# 确保备份目录存在。
mkdir -p "${BACKUP_DIR}"

echo "[backup] started. interval=${BACKUP_INTERVAL_SECONDS}s retention=${BACKUP_RETENTION_DAYS}d"

while true; do
  # 备份文件名带时间戳，避免覆盖。
  TS="$(date +%Y%m%d_%H%M%S)"
  FILE="${BACKUP_DIR}/${POSTGRES_DB}_${TS}.dump"

  if pg_dump \
    -h "${POSTGRES_HOST}" \
    -p "${POSTGRES_PORT}" \
    -U "${POSTGRES_USER}" \
    -d "${POSTGRES_DB}" \
    -F c \
    -f "${FILE}"; then
    echo "[backup] created ${FILE}"
  else
    # 备份失败时删除可能产生的残缺文件。
    echo "[backup] failed at ${TS}"
    rm -f "${FILE}"
  fi

  # 清理超过保留天数的历史备份。
  find "${BACKUP_DIR}" -type f -name "${POSTGRES_DB}_*.dump" -mtime +"${BACKUP_RETENTION_DAYS}" -delete || true
  sleep "${BACKUP_INTERVAL_SECONDS}"
done
