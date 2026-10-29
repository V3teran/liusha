#!/bin/bash
# liusha 单镜像入口：进程托管（initdb → migrate → proxy/runner/api/nginx）。
#
# 设计取舍（单容器全家桶的约定）：
#   - 任一子进程退出 → 整体关停退出（exit code 1），交给容器编排器重启；
#     不做容器内自愈——半瘫的平台（如 runner 死了）比显式宕机更难排查。
#   - 首次启动生成的密钥（API key / SSE cookie / LLM 加密密钥）持久化在
#     /data/secrets 下，重启不变；env 显式给值则优先生效。
#   - PG 仅监听 127.0.0.1（不对外发布，容器外不可达）；liusha 角色为超级用户
#     ——与 dev compose 的 POSTGRES_USER=liusha 引导形态一致（迁移里 CREATE
#     EXTENSION 需要超管权限）。
set -euo pipefail

# ── 布局与环境 ────────────────────────────────────────────────────────────
DATA_DIR="${LIUSHA_DATA_DIR:-/data}"
PGDATA="$DATA_DIR/pg"
REDIS_DIR="$DATA_DIR/redis"
CERT_DIR="$DATA_DIR/certs"
LOG_DIR="$DATA_DIR/logs"
SECRETS_DIR="$DATA_DIR/secrets"
DSN="postgres://liusha:liusha@127.0.0.1:5432/liusha?sslmode=disable"

export LIUSHA_POSTGRES_DSN="$DSN"
export LIUSHA_REDIS_ADDR="127.0.0.1:6379"
export LIUSHA_LOG_DIR="$LOG_DIR"
export LIUSHA_PROXY_CERT_DIR="$CERT_DIR"
export LIUSHA_CONFIG="/app/config/config.yaml"

mkdir -p "$PGDATA" "$REDIS_DIR" "$CERT_DIR" "$LOG_DIR" "$SECRETS_DIR"
# pg_ctl 以 postgres 用户身份起库/写日志，PGDATA 与日志目录须归 postgres
chown -R postgres:postgres "$PGDATA" "$LOG_DIR"

log() { echo "[entrypoint] $*"; }

# ── 首启密钥（env 优先，否则生成并持久化）──────────────────────────────
secret_or_gen() { # $1=env名 $2=文件名 $3=生成命令
  local env_val file="$SECRETS_DIR/$2"
  eval "env_val=\${$1:-}"
  if [ -n "$env_val" ]; then
    echo "$env_val"
  elif [ -s "$file" ]; then
    cat "$file"
  else
    local val
    # 生成器是完整 shell 片段（含管道），必须 eval 整串执行——$($3) 会按词拆分
    val="$(eval "$3")"
    [ -n "$val" ] || { echo "[entrypoint] 密钥生成失败：$2" >&2; exit 1; }
    echo -n "$val" > "$file"
    chmod 600 "$file"
    echo "$val"
  fi
}
export LIUSHA_API_KEY="$(secret_or_gen LIUSHA_API_KEY api.key "head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n'")"
export LIUSHA_STREAM_COOKIE_SECRET="$(secret_or_gen LIUSHA_STREAM_COOKIE_SECRET stream_cookie.secret "head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'")"
# LLM 密钥加密要求 32 字节 hex（cryptx fail-fast 口径）
export LIUSHA_LLM_KEY_SECRET="$(secret_or_gen LIUSHA_LLM_KEY_SECRET llm_key.secret "head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'")"
log "API key: $LIUSHA_API_KEY（已存 $SECRETS_DIR/api.key，env LIUSHA_API_KEY 可覆盖）"

# ── 进程托管 ──────────────────────────────────────────────────────────────
children=()
spawn() { # $1=显示名 其余=命令
  local name="$1"; shift
  "$@" &
  children+=("$!")
  log "started $name (pid $!)"
}
shutdown() {
  log "shutting down (${children[*]:-无子进程})"
  for pid in "${children[@]:-}"; do
    [ -n "$pid" ] && kill -TERM "$pid" 2>/dev/null || true
  done
  # PG 不走 children（postgres 用户身份），单独优雅停
  su postgres -c "pg_ctl -D '$PGDATA' -m fast stop" 2>/dev/null || true
  wait 2>/dev/null || true
  exit 0
}
trap 'shutdown' TERM INT

# ── 1. PostgreSQL：首启 initdb，角色/库幂等建 ─────────────────────────────
if [ ! -s "$PGDATA/PG_VERSION" ]; then
  log "initdb（首启）…"
  chown -R postgres:postgres "$PGDATA"
  su postgres -c "initdb -D '$PGDATA' -E UTF8 --auth=trust" >/dev/null
fi
chown -R postgres:postgres "$PGDATA"
su postgres -c "pg_ctl -D '$PGDATA' -l '$LOG_DIR/pg.log' -o '-p 5432 -c listen_addresses=127.0.0.1' -w start" >/dev/null
until su postgres -c "pg_isready -q -p 5432"; do sleep 0.3; done
# liusha 超级用户 + 库（幂等；与 dev compose 的 POSTGRES_USER=liusha 同构）
su postgres -c "psql -q -p 5432 -tc \"SELECT 1 FROM pg_roles WHERE rolname='liusha'\" | grep -q 1" \
  || su postgres -c "psql -q -p 5432 -c 'CREATE ROLE liusha LOGIN SUPERUSER'"
su postgres -c "psql -q -p 5432 -tc \"SELECT 1 FROM pg_database WHERE datname='liusha'\" | grep -q 1" \
  || su postgres -c "createdb -p 5432 -O liusha liusha"
log "postgres ready (127.0.0.1:5432)"

# ── 2. Redis（AOF 落 /data：队列态与凭证库在此，容器重启不丢）──────────
spawn redis redis-server --port 6379 --bind 127.0.0.1 --dir "$REDIS_DIR" --appendonly yes

# ── 3. 迁移（同一 CLI/版本口径；空库首启建全量表结构）───────────────────
/app/migrate -path /app/migrations -database "$DSN" up
log "migrations up to date"

# ── 4. 业务进程 ───────────────────────────────────────────────────────────
if [ ! -S /var/run/docker.sock ]; then
  log "警告：未挂载 /var/run/docker.sock——runner 无法拉起沙盒容器，启动后将 Fatal 退出"
fi
spawn proxy /app/proxy
spawn runner /app/runner
spawn api /app/api
spawn nginx nginx -c /etc/nginx/liusha-single.conf -g 'daemon off;'

# ── 5. 等任一子进程退出 → 整体关停（共生共死）────────────────────────────
log "all services up（web=80 mitm=8888 healthz/ingest=9090）"
set +e
wait -n
rc=$?
log "子进程退出（rc=$rc），整体关停"
shutdown
