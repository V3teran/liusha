# syntax=docker/dockerfile:1
# liusha api 镜像：go vendor 构建 + alpine runtime。
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY . .
ENV CGO_ENABLED=0
RUN go build -mod=vendor -o /out/api ./cmd/api

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=build /out/api /app/api
COPY config /app/config
EXPOSE 8080
ENTRYPOINT ["/app/api"]
