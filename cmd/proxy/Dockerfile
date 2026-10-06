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
EXPOSE 8888
ENTRYPOINT ["/app/proxy"]
