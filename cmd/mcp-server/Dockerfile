# 阶段一：构建 liusha-mcp-server 二进制（离线 vendor 模式）
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ ./vendor/
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -trimpath -ldflags="-s -w" \
    -o /out/liusha-mcp-server ./cmd/mcp-server

# 阶段二：最终运行镜像（与 cmd/api/Dockerfile 保持一致）
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/liusha-mcp-server /usr/local/bin/liusha-mcp-server
COPY config/ /app/config/
ENV LIUSHA_CONFIG=/app/config/config.yaml
ENTRYPOINT ["/usr/local/bin/liusha-mcp-server"]
