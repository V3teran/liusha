# vuln-tools：nuclei + ffuf + httpx 扫描工具箱
# 工具通过 Go install 编译；运行时镜像仅保留二进制和 CA 证书。
FROM golang:1.25-alpine AS tool-builder
RUN apk add --no-cache git ca-certificates
RUN go install github.com/projectdiscovery/nuclei/v3/cmd/nuclei@latest \
 && go install github.com/ffuf/ffuf/v2@latest \
 && go install github.com/projectdiscovery/httpx/cmd/httpx@latest

FROM golang:1.25-alpine
RUN apk add --no-cache ca-certificates
COPY --from=tool-builder /go/bin/nuclei  /usr/local/bin/nuclei
COPY --from=tool-builder /go/bin/ffuf    /usr/local/bin/ffuf
COPY --from=tool-builder /go/bin/httpx   /usr/local/bin/httpx
# mcp-server 通过 docker exec 调用工具；容器需保持运行。
CMD ["sleep", "infinity"]
