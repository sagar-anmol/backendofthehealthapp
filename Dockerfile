# Build stage
FROM golang:alpine AS builder

WORKDIR /app
RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /hosti-server ./cmd/server

# Final lightweight runtime stage
FROM alpine:latest

WORKDIR /app
RUN apk add --no-cache ca-certificates

COPY --from=builder /hosti-server /app/hosti-server

ENV PORT=8080
ENV DB_PATH=/app/data/hosti.db
ENV PRIMARY_PROVIDER=gemini

EXPOSE 8080

CMD ["/app/hosti-server"]
