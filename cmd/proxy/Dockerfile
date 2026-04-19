# 阶段一：构建 liusha-proxy 二进制（离线 vendor 模式）
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ ./vendor/
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -trimpath -ldflags="-s -w" -o /out/liusha-proxy ./cmd/proxy

# 阶段二：最终运行镜像（与 cmd/api/Dockerfile 保持一致）
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/liusha-proxy /usr/local/bin/liusha-proxy
# proxify 为预编译的 linux/amd64 静态二进制，由 bin/proxify 提供
COPY bin/proxify /usr/local/bin/proxify
COPY config/ /app/config/
ENV LIUSHA_CONFIG=/app/config/config.yaml
ENTRYPOINT ["/usr/local/bin/liusha-proxy"]
