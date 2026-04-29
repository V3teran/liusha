FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY . .
ENV CGO_ENABLED=0
RUN go build -mod=vendor -o /out/vulnapp ./cmd/vulnapp

FROM alpine:3.20
WORKDIR /app
COPY --from=build /out/vulnapp /app/vulnapp
EXPOSE 8001
ENTRYPOINT ["/app/vulnapp"]
