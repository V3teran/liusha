# syntax=docker/dockerfile:1
# liusha 前置 nginx 镜像：多阶段——Stage 0 用 node+pnpm 构建前端，Stage 1 nginx 托管 dist。
# build context = 仓库根（compose 里 context: ..），故 COPY 路径以 web/ 开头。

# ── Stage 0: 构建前端（web/ 源码 → dist/）──
FROM node:22-alpine AS web-build
RUN corepack enable
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
