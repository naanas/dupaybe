FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /dupay-api ./cmd/api

FROM gcr.io/distroless/static-debian12

WORKDIR /app
COPY --from=builder /dupay-api /dupay-api

ENV PORT=8080
EXPOSE 8080

ENTRYPOINT ["/dupay-api"]
