#（无 syntax= 指令：仅用基础语法，内置 BuildKit 前端足够，构建免连 Docker Hub）
# liusha proxy 镜像：go vendor 构建 + alpine runtime + 8888 mitm（纯 MITM，无 healthz HTTP）。
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY . .
ENV CGO_ENABLED=0
RUN go build -mod=vendor -o /out/proxy ./cmd/proxy

FROM alpine:3.20
# ca-certificates：MITM 上游 TLS 校验。healthcheck 用 busybox 内置 nc 探 8888，无需额外装包。
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /out/proxy /app/proxy
COPY config /app/config
# non-root 运行（密码文件无 shell 登录；app 目录属主移交）。
# /app/certs 预建并归 app：MITM CA 卷（liusha-proxy-certs）首挂时继承此属主，
# 否则 app 用户无法生成 CA（曾回归：certs 卷 root 属主 → proxy 启动循环崩溃）。
RUN addgroup -S app && adduser -S app -G app -h /app
RUN mkdir -p /app/certs && chown -R app:app /app
USER app
EXPOSE 8888
ENTRYPOINT ["/app/proxy"]
