
FROM golang:1.25-alpine AS builder
WORKDIR /app
RUN apk add --no-cache build-base
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-extldflags=-static" -o /trello-gcal-sync

# Final stage
FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /trello-gcal-sync .
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=10s --start-interval=5s --retries=3 \
  CMD curl -f http://localhost:8080/api/health || exit 1
CMD ["./trello-gcal-sync"]
