# syntax=docker/dockerfile:1
# liusha proxy 镜像：go vendor 构建 + alpine runtime + 8888 mitm + 9091 healthz。
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY . .
ENV CGO_ENABLED=0
RUN go build -mod=vendor -o /out/proxy ./cmd/proxy

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=build /out/proxy /app/proxy
COPY config /app/config
EXPOSE 8888 9091
ENTRYPOINT ["/app/proxy"]
