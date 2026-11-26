# Go Distributed CDN Makefile

.PHONY: all build test clean run-all stop-all logs

all: build test

build:
	@echo "Building all Go services..."
	@go build ./...

test:
	@echo "Running tests..."
	@go test -v ./...
	@go test -v ./tests/...

run-all:
	@echo "Launching all 5 CDN servers and MongoDB instances with Docker Compose..."
	docker compose up -d --build
	@echo "\nAll services started!"
	@echo "Server 1 (Webapp):      http://localhost:3000"
	@echo "Server 2 (Central API): http://localhost:4000"
	@echo "Server 3 (Edge SG):     http://localhost:5001"
	@echo "Server 4 (Edge NY):     http://localhost:5002"
	@echo "Server 5 (Edge LDN):    http://localhost:5003"

stop-all:
	@echo "Stopping all CDN servers..."
	docker compose down

logs:
	docker compose logs -f

clean:
	@rm -rf bin/
