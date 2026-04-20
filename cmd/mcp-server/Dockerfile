# 阶段一：构建 liusha-mcp-server 二进制（离线 vendor 模式）
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ ./vendor/
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -trimpath -ldflags="-s -w" \
    -o /out/liusha-mcp-server ./cmd/mcp-server

# 阶段二：工具构建（nuclei / ffuf / httpx 直接编译为静态二进制）
FROM golang:1.25-alpine AS tool-builder
RUN apk add --no-cache git ca-certificates
RUN go install github.com/projectdiscovery/nuclei/v3/cmd/nuclei@latest \
 && go install github.com/ffuf/ffuf/v2@latest \
 && go install github.com/projectdiscovery/httpx/cmd/httpx@latest

# 阶段三：最终运行镜像。改用 alpine 而非 distroless，
# 是因为工具（尤其 nuclei）运行时需要 dns / ca 证书等最小运行环境。
# 同时消除对宿主机 docker.sock 的依赖（见 P3-3），nonroot 用户执行。
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tini \
 && addgroup -S liusha && adduser -S -G liusha liusha
COPY --from=builder /out/liusha-mcp-server /usr/local/bin/liusha-mcp-server
COPY --from=tool-builder /go/bin/nuclei  /usr/local/bin/nuclei
COPY --from=tool-builder /go/bin/ffuf    /usr/local/bin/ffuf
COPY --from=tool-builder /go/bin/httpx   /usr/local/bin/httpx
COPY config/ /app/config/
ENV LIUSHA_CONFIG=/app/config/config.yaml
USER liusha
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/liusha-mcp-server"]
