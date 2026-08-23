# syntax=docker/dockerfile:1
# liusha runner 镜像：go vendor 构建 + alpine runtime + 9090 healthz。
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY . .
ENV CGO_ENABLED=0
RUN go build -mod=vendor -o /out/runner ./cmd/runner

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=build /out/runner /app/runner
COPY config /app/config
COPY skills /app/skills
EXPOSE 9090
ENTRYPOINT ["/app/runner"]
