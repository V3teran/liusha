# syntax=docker/dockerfile:1
# liusha 前置 nginx 镜像：多阶段——Stage 0 用 node+pnpm 构建前端，Stage 1 nginx 托管 dist。
# build context = 仓库根（compose 里 context: ..），故 COPY 路径以 web/ 开头。

# ── Stage 0: 构建前端（web/ 源码 → dist/）──
FROM node:22-alpine AS web-build
RUN corepack enable
# CI=true：pnpm 在非交互式环境（无 TTY）下，COPY 覆盖文件后会做依赖状态检查并尝试确认清空
# node_modules 重装，无 TTY 时直接报 ERR_PNPM_ABORTED_REMOVE_MODULES_DIR_NO_TTY 失败。
ENV CI=true
WORKDIR /web
# 先拷锁文件，命中依赖层缓存。
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml web/.npmrc ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

# ── Stage 1: nginx 托管 ──
FROM nginx:1.27-alpine
COPY deployments/nginx/nginx.conf /etc/nginx/nginx.conf
COPY --from=web-build /web/dist /usr/share/nginx/html
EXPOSE 80
