FROM golang:1.24-alpine AS builder

WORKDIR /app

RUN apk add --no-cache gcc musl-dev

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o load-balancer ./cmd/load-balancer/main.go

FROM alpine:latest

WORKDIR /app

COPY --from=builder /app/load-balancer .
COPY .env .env
COPY config.yaml config.yaml

RUN apk add --no-cache ca-certificates

RUN chmod +x /app/load-balancer

ENTRYPOINT ["./load-balancer"]