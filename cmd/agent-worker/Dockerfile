# syntax=docker/dockerfile:1
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ ./vendor/
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -trimpath -ldflags="-s -w" \
    -o /out/agent-worker ./cmd/agent-worker

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=builder /out/agent-worker /app/agent-worker
COPY config/ /app/config/
ENV LIUSHA_CONFIG=/app/config/config.yaml
EXPOSE 9090
ENTRYPOINT ["/app/agent-worker"]
