# syntax=docker/dockerfile:1
# 多阶段构建：builder 阶段用官方 golang:1.25-alpine 编译静态二进制；
# go.mod 声明的是 go 1.25.6，按计划要求 "若 go.mod 版本更高必须上调" 选择 1.25-alpine。
FROM golang:1.25-alpine AS builder
WORKDIR /src

# 先只复制 go.mod/go.sum 以利用 Docker 层缓存：依赖不变则跳过 go mod download。
COPY go.mod go.sum ./
RUN go mod download

# 再复制全部源码，执行静态编译：-trimpath 去除本地路径，-s -w 去掉符号表减小体积。
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# 运行阶段使用 distroless/static-debian12:nonroot：无 shell、无包管理器，默认以 nonroot 运行。
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=builder /out/api /app/api
# 把配置与迁移脚本一并带入镜像，避免运行时再挂卷。
COPY config/ /app/config/
COPY db/migrations/ /app/db/migrations/
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app/api"]
