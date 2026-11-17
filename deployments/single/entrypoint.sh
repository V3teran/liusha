#!/bin/bash
# liusha 单镜像进程托管：PostgreSQL → Redis → liusha serve（serve 启动前自动迁移数据库）。
#
#   - 配置文件是 /data/config.yaml（部署方把宿主上的 config.yaml 只读挂进来）；没有就不启动。
#   - 密钥与密码来自 .env（docker run --env-file .env），与多镜像同一份、同一组变量；缺了就不启动。
#   - 任一进程退出 → 整体停止（退出码非 0），交给容器编排器重启；不做容器内自愈。
#   - PostgreSQL / Redis 只监听 127.0.0.1，容器外不可达；两者都要密码（沙盒命令与它们同在一个容器）。
set -euo pipefail

DATA_DIR=/data
PGDATA="$DATA_DIR/pg"
PG_MAJOR=18
PG_BIN="/usr/lib/postgresql/$PG_MAJOR/bin"

log() { echo "[liusha-single] $*"; }

# 先查配置文件与 .env：缺了 liusha 起不来，不必先拉起数据库再失败。
if [ ! -f "$LIUSHA_CONFIG" ]; then
  log "缺少配置文件 $LIUSHA_CONFIG：把同版本的 config.yaml 放到宿主 ~/data/liusha/config.yaml，运行时加"
  log "  --mount type=bind,src=\$HOME/data/liusha/config.yaml,dst=/data/config.yaml,readonly"
  exit 1
fi
missing=()
for name in LIUSHA_POSTGRES_PASSWORD LIUSHA_REDIS_PASSWORD LIUSHA_SANDBOX_LISTENER_PORTS LIUSHA_SANDBOX_TOKEN LIUSHA_LLM_ENCRYPTION_KEY LIUSHA_ADMIN_INITIAL_PASSWORD; do
  [ -n "${!name:-}" ] || missing+=("$name")
done
if [ ${#missing[@]} -gt 0 ]; then
  log "缺少环境变量：${missing[*]}。由仓库的 .env.example 复制为 .env 填写，运行时加 --env-file .env"
  exit 1
fi
# 两个密码要拼进连接串：只接受无需转义的字符（openssl rand -hex 生成的即可）。
for name in LIUSHA_POSTGRES_PASSWORD LIUSHA_REDIS_PASSWORD; do
  if [[ ! "${!name}" =~ ^[A-Za-z0-9._~-]+$ ]]; then
    log "$name 含需要转义的字符：请用 openssl rand -hex 16 生成"
    exit 1
  fi
done

mkdir -p "$PGDATA" "$DATA_DIR/redis" "$DATA_DIR/logs"
chown -R postgres:postgres "$PGDATA"
# PostgreSQL 以 postgres 用户写日志；日志目录本身仍归 root（liusha 写在其中）。
touch "$DATA_DIR/logs/postgres.log"
chown postgres:postgres "$DATA_DIR/logs/postgres.log"

# 容器内的拓扑由本脚本给出：数据目录固定为 /data（.env 里给多镜像用的 LIUSHA_DATA_DIR 在这里不适用），
# 连接串按内置数据库的密码拼出。
export LIUSHA_DATA_DIR="$DATA_DIR"
export LIUSHA_POSTGRES_URL="postgres://liusha:$LIUSHA_POSTGRES_PASSWORD@127.0.0.1:5432/liusha?sslmode=disable"
export LIUSHA_REDIS_URL="redis://:$LIUSHA_REDIS_PASSWORD@127.0.0.1:6379/0"
# 控制台「代理接入」显示的代理地址：容器内的 8588 按 LIUSHA_PROXY_HOST_PORT 发布到宿主（与 make single-run
# 同一缺省），主机由控制台补为它自己的主机；.env 写了完整地址时以它为准。
export LIUSHA_PROXY_PUBLIC_ADDR="${LIUSHA_PROXY_PUBLIC_ADDR:-:${LIUSHA_PROXY_HOST_PORT:-58588}}"

# ── PostgreSQL ──
if [ -s "$PGDATA/PG_VERSION" ] && [ "$(cat "$PGDATA/PG_VERSION")" != "$PG_MAJOR" ]; then
  log "数据目录是 PostgreSQL $(cat "$PGDATA/PG_VERSION") 格式，本镜像使用 PostgreSQL $PG_MAJOR，无法直接打开。"
  log "请先用旧镜像导出数据再导入新镜像，步骤见 deployments/README.md「单镜像升级」。"
  exit 1
fi
if [ ! -s "$PGDATA/PG_VERSION" ]; then
  log "初始化数据库…"
  su postgres -c "$PG_BIN/initdb -D '$PGDATA' -E UTF8 --auth-local=peer --auth-host=scram-sha-256" >/dev/null
fi
# 认证规则每次启动按本镜像的约定写定：本机 socket 只许 postgres 系统用户（peer，供本脚本建库、
# 设密码），TCP 一律凭密码。
cat > "$PGDATA/pg_hba.conf" <<'EOF'
local all postgres peer
host  all all 127.0.0.1/32 scram-sha-256
host  all all ::1/128      scram-sha-256
EOF
chown postgres:postgres "$PGDATA/pg_hba.conf"
su postgres -c "$PG_BIN/pg_ctl -D '$PGDATA' -l '$DATA_DIR/logs/postgres.log' -o '-p 5432 -c listen_addresses=127.0.0.1' -w start" >/dev/null
# liusha 角色为超级用户：迁移中 CREATE EXTENSION vector 需要。密码每次启动按 .env 设定
# （上面已校验只含无需转义的字符）。
su postgres -c "psql -q -p 5432 -tc \"SELECT 1 FROM pg_roles WHERE rolname='liusha'\" | grep -q 1" \
  || su postgres -c "psql -q -p 5432 -c 'CREATE ROLE liusha LOGIN SUPERUSER'"
su postgres -c "psql -q -p 5432 -v pw=$LIUSHA_POSTGRES_PASSWORD" <<'EOF'
ALTER ROLE liusha PASSWORD :'pw';
EOF
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

redis-server --port 6379 --bind 127.0.0.1 --dir "$DATA_DIR/redis" --appendonly yes --requirepass "$LIUSHA_REDIS_PASSWORD" &
children+=("$!")
# 等 Redis 可用（载入 AOF 期间拒绝命令）：liusha serve 启动即连 Redis，连不上整体退出。
until REDISCLI_AUTH="$LIUSHA_REDIS_PASSWORD" redis-cli -h 127.0.0.1 -p 6379 ping 2>/dev/null | grep -q PONG; do
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
