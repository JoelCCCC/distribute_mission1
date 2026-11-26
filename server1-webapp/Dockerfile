FROM golang:1.24-alpine AS builder
WORKDIR /app

# Download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Build server1 binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o webapp ./server1-webapp

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app

COPY --from=builder /app/webapp .
COPY server1-webapp/static ./static

EXPOSE 3000
CMD ["./webapp"]
