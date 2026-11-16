#!/usr/bin/env bash
# scripts/dev/env.sh — 开发脚本共用：读仓库根 .env，导出宿主上直接运行 liusha 组件所需的变量。
# 用法（在仓库根）：. scripts/dev/env.sh
#
# 开发形态：pg / redis / 沙盒跑在容器里（scripts/dev/up.sh），组件在宿主上运行（run-svc.sh），
# 经宿主端口访问容器。.env 按容器部署填写（LIUSHA_POSTGRES_PASSWORD、LIUSHA_REDIS_PASSWORD、
# LIUSHA_SANDBOX_LISTENER_PORTS 与密钥），宿主侧的连接串在这里按宿主端口拼出。

set -a
# shellcheck disable=SC1091
[ -f .env ] && . ./.env
set +a

# 数据目录与配置文件：与沙盒容器同一路径（compose 以同一绝对路径挂载）。
export LIUSHA_DATA_DIR="${LIUSHA_DATA_DIR:-$HOME/data/liusha}"
export LIUSHA_CONFIG="$LIUSHA_DATA_DIR/config.yaml"

# 宿主侧地址（与 compose.yaml 的宿主端口映射一致：pg 55432、redis 56379、沙盒 58591）。
export LIUSHA_POSTGRES_URL="postgres://liusha:${LIUSHA_POSTGRES_PASSWORD:?LIUSHA_POSTGRES_PASSWORD 未设置（.env 必填）}@localhost:${LIUSHA_POSTGRES_HOST_PORT:-55432}/liusha?sslmode=disable"
export LIUSHA_REDIS_URL="redis://:${LIUSHA_REDIS_PASSWORD:?LIUSHA_REDIS_PASSWORD 未设置（.env 必填）}@localhost:${LIUSHA_REDIS_HOST_PORT:-56379}/0"
export LIUSHA_SANDBOX_URL="http://127.0.0.1:${LIUSHA_SANDBOX_HOST_PORT:-58591}"
# 沙盒容器按监听段发布端口，宿主上的 runner 按同一段分配（.env 必填，与容器部署同一项）。
: "${LIUSHA_SANDBOX_LISTENER_PORTS:?LIUSHA_SANDBOX_LISTENER_PORTS 未设置（.env 必填）}"
# 初始管理员：账号表为空时 api 用它建 admin（仅首启生效），e2e 也用它登录。dev 用固定值方便记忆。
export LIUSHA_ADMIN_INITIAL_PASSWORD="${LIUSHA_ADMIN_INITIAL_PASSWORD:-changeme-admin-pass}"
