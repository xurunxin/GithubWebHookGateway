FROM golang:1.24-alpine AS builder

WORKDIR /app

ENV GOTOOLCHAIN=auto

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /out/webhook-relay ./cmd/server

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /out/webhook-relay /webhook-relay
COPY --from=builder /app/migrations/ /migrations/

VOLUME ["/data"]

EXPOSE 8080

ENTRYPOINT ["/webhook-relay"]
