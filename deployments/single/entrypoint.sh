#!/bin/bash
# liusha 单镜像进程托管：PostgreSQL → Redis → liusha serve（serve 启动前自动迁移数据库）。
#
#   - 任一进程退出 → 整体停止（退出码非 0），交给容器编排器重启；不做容器内自愈。
#   - 首次启动生成的密钥持久化在 /data/secrets，重启不变；同名环境变量显式给值时优先。
#   - PostgreSQL / Redis 只监听 127.0.0.1，容器外不可达。
set -euo pipefail

DATA_DIR=/data
PGDATA="$DATA_DIR/pg"
PG_MAJOR=18
PG_BIN="/usr/lib/postgresql/$PG_MAJOR/bin"
SECRETS_DIR="$DATA_DIR/secrets"

log() { echo "[liusha-single] $*"; }

mkdir -p "$PGDATA" "$DATA_DIR/redis" "$DATA_DIR/workspace" "$DATA_DIR/logs" "$DATA_DIR/certs" "$SECRETS_DIR"
chmod 700 "$SECRETS_DIR"
chown -R postgres:postgres "$PGDATA"
# PostgreSQL 以 postgres 用户写日志；日志目录本身仍归 root（liusha 写在其中）。
touch "$DATA_DIR/logs/postgres.log"
chown postgres:postgres "$DATA_DIR/logs/postgres.log"

# secret <环境变量> <文件> <生成命令>：环境变量优先，其次持久化文件，否则生成并持久化。
secret() {
  local name="$1" file="$SECRETS_DIR/$2" gen="$3" val="${!1:-}"
  if [ -z "$val" ]; then
    if [ -s "$file" ]; then
      val="$(cat "$file")"
    else
      val="$(eval "$gen")"
      [ -n "$val" ] || { log "生成密钥失败：$2"; exit 1; }
      printf '%s' "$val" > "$file"
      chmod 600 "$file"
    fi
  fi
  export "$name=$val"
}
hex() { head -c "$1" /dev/urandom | od -An -tx1 | tr -d ' \n'; }

secret LIUSHA_ADMIN_INITIAL_PASSWORD admin.initial_password "head -c 18 /dev/urandom | base64 | tr -d '+/='"
secret LIUSHA_LLM_KEY_SECRET llm_key.secret "hex 32"
secret REDIS_PASSWORD redis.password "hex 24"
secret LIUSHA_SANDBOX_TOKEN sandbox.token "hex 32"
# 密码不进日志（容器日志长期留存、随日志采集外传）：只给出位置。
if [ -s "$SECRETS_DIR/admin.initial_password" ]; then
  log "初始管理员 admin 的密码在 $SECRETS_DIR/admin.initial_password（仅首次启动生效，登录后请改密）"
fi

export LIUSHA_POSTGRES_DSN="postgres://liusha@127.0.0.1:5432/liusha?sslmode=disable"
export LIUSHA_REDIS_ADDR=127.0.0.1:6379
export LIUSHA_REDIS_PASSWORD="$REDIS_PASSWORD"
export LIUSHA_SANDBOX_WORKSPACE_DIR="$DATA_DIR/workspace"
export LIUSHA_LOG_DIR="$DATA_DIR/logs"
export LIUSHA_PROXY_CERT_DIR="$DATA_DIR/certs"

# ── PostgreSQL ──
if [ -s "$PGDATA/PG_VERSION" ] && [ "$(cat "$PGDATA/PG_VERSION")" != "$PG_MAJOR" ]; then
  log "数据目录是 PostgreSQL $(cat "$PGDATA/PG_VERSION") 格式，本镜像使用 PostgreSQL $PG_MAJOR，无法直接打开。"
  log "请先用旧镜像导出数据再导入新镜像，步骤见 deployments/README.md「单镜像升级」。"
  exit 1
fi
if [ ! -s "$PGDATA/PG_VERSION" ]; then
  log "初始化数据库…"
  su postgres -c "$PG_BIN/initdb -D '$PGDATA' -E UTF8 --auth=trust" >/dev/null
fi
su postgres -c "$PG_BIN/pg_ctl -D '$PGDATA' -l '$DATA_DIR/logs/postgres.log' -o '-p 5432 -c listen_addresses=127.0.0.1' -w start" >/dev/null
# liusha 角色为超级用户：迁移中 CREATE EXTENSION vector 需要
su postgres -c "psql -q -p 5432 -tc \"SELECT 1 FROM pg_roles WHERE rolname='liusha'\" | grep -q 1" \
  || su postgres -c "psql -q -p 5432 -c 'CREATE ROLE liusha LOGIN SUPERUSER'"
su postgres -c "psql -q -p 5432 -tc \"SELECT 1 FROM pg_database WHERE datname='liusha'\" | grep -q 1" \
  || su postgres -c "createdb -p 5432 -O liusha liusha"
log "PostgreSQL 就绪"

# ── 进程托管 ──
children=()
shutdown() {
  for pid in "${children[@]}"; do kill -TERM "$pid" 2>/dev/null || true; done
  wait 2>/dev/null || true
  su postgres -c "$PG_BIN/pg_ctl -D '$PGDATA' -m fast stop" >/dev/null 2>&1 || true
}
trap 'shutdown; exit 0' TERM INT

redis-server --port 6379 --bind 127.0.0.1 --dir "$DATA_DIR/redis" --appendonly yes --requirepass "$REDIS_PASSWORD" &
children+=("$!")
# 等 Redis 可用（载入 AOF 期间拒绝命令）：liusha serve 启动即连 Redis，连不上整体退出。
until REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli -h 127.0.0.1 -p 6379 ping 2>/dev/null | grep -q PONG; do
  kill -0 "${children[0]}" 2>/dev/null || { log "Redis 启动失败"; shutdown; exit 1; }
  sleep 0.2
done
log "Redis 就绪"
liusha serve &
children+=("$!")
log "已启动：控制台与 API :8580，MITM 代理 :8588"

set +e
wait -n
rc=$?
log "进程退出（rc=$rc），整体停止"
shutdown
exit $(( rc == 0 ? 1 : rc ))
