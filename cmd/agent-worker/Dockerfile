# syntax=docker/dockerfile:1
# liusha agent-worker 镜像：go vendor 构建 + alpine runtime + 9090 healthz。
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY . .
ENV CGO_ENABLED=0
RUN go build -mod=vendor -o /out/agent-worker ./cmd/agent-worker

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=build /out/agent-worker /app/agent-worker
COPY config /app/config
COPY skills /app/skills
# SKILL.md frontmatter 的 cognitive_map: docs/skills/bac/cognitive_map.md 是相对 cwd（容器内 /app），
# loader 启动校验需要这个路径文件存在。
COPY docs/skills /app/docs/skills
EXPOSE 9090
ENTRYPOINT ["/app/agent-worker"]
