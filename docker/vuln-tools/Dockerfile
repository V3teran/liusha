# vuln-tools：nuclei + ffuf + httpx 一次性沙箱镜像
#
# 运行模式：每次 agent-worker 通过 docker API 新建 --rm 容器，
# 指定 cmd = [nuclei|ffuf|httpx, <args...>]，跑完立即销毁。
# 不设 ENTRYPOINT / CMD —— 由调用方显式提供命令。
FROM golang:1.25-alpine AS tool-builder
RUN apk add --no-cache git ca-certificates
RUN go install github.com/projectdiscovery/nuclei/v3/cmd/nuclei@latest \
 && go install github.com/ffuf/ffuf/v2@latest \
 && go install github.com/projectdiscovery/httpx/cmd/httpx@latest

# 运行时镜像：alpine + tini + 非 root 用户，便于 agent-worker 强制 --user=nobody。
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tini
COPY --from=tool-builder /go/bin/nuclei  /usr/local/bin/nuclei
COPY --from=tool-builder /go/bin/ffuf    /usr/local/bin/ffuf
COPY --from=tool-builder /go/bin/httpx   /usr/local/bin/httpx
# tini 作为 init 处理 SIGTERM，短命容器优雅退出。
ENTRYPOINT ["/sbin/tini", "--"]
# 不设 CMD —— 调用方（agent-worker DockerRunner）通过 ContainerCreate.Cmd 注入。
