# wa-engine Makefile
# Usage:
#   make mac          - build server binary for macOS (current arch)
#   make linux        - cross-compile for linux/amd64
#   make windows      - cross-compile for windows/amd64
#   make android      - build Android AAR via Docker
#   make run          - build + run locally on port 8080
#   make test-send    - quick smoke test (requires running server + jq)
#   make clean        - remove build artifacts

MODULE     := github.com/mml/wa-engine
SERVER_PKG := ./cmd/server
OUT_DIR    := ./build
DATA_DIR   := ./wa-data

.PHONY: all mac linux windows android run test-send clean

all: mac

# ─── Desktop / Server builds ──────────────────────────────────────────────────

mac:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=1 go build -ldflags="-s -w" -o $(OUT_DIR)/wa-server ./cmd/server
	@echo "✓ Built $(OUT_DIR)/wa-server (macOS)"

linux:
	@mkdir -p $(OUT_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 \
	  CC=x86_64-linux-musl-gcc \
	  go build -ldflags="-s -w" -o $(OUT_DIR)/wa-server-linux ./cmd/server
	@echo "✓ Built $(OUT_DIR)/wa-server-linux"

# Windows cross-compile requires mingw: brew install mingw-w64
windows:
	@mkdir -p $(OUT_DIR)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 \
	  CC=x86_64-w64-mingw32-gcc \
	  go build -ldflags="-s -w" -o $(OUT_DIR)/wa-server.exe ./cmd/server
	@echo "✓ Built $(OUT_DIR)/wa-server.exe"

# ─── Android AAR via Docker ───────────────────────────────────────────────────

android:
	@mkdir -p $(OUT_DIR)
	docker build --platform=linux/amd64 -f bindings/android/Dockerfile.aar-build -t wa-engine-builder .
	docker run --platform=linux/amd64 --rm \
	  -v "$(PWD)/$(OUT_DIR):/workspace/bindings/android/build" \
	  wa-engine-builder
	@echo "✓ Built $(OUT_DIR)/waengine.aar"

# ─── Local run ────────────────────────────────────────────────────────────────

run: mac
	$(OUT_DIR)/wa-server --port 8080 --data $(DATA_DIR)

# Run with an API key (more secure)
run-secure: mac
	$(OUT_DIR)/wa-server --port 8080 --data $(DATA_DIR) --key my-secret-key

# ─── Smoke tests (requires server running + jq installed) ─────────────────────

test-send:
	@echo "=== Health check ==="
	curl -s http://localhost:8080/api/health | jq .

	@echo "\n=== List sessions ==="
	curl -s http://localhost:8080/api/sessions | jq .

	@echo "\n=== Start pairing (session: test) ==="
	curl -s -X POST http://localhost:8080/api/sessions/test/pair | jq .

	@echo "\n=== Poll QR ==="
	@sleep 1
	curl -s http://localhost:8080/api/sessions/test/qr | jq .

# ─── Maintenance ──────────────────────────────────────────────────────────────

clean:
	rm -rf $(OUT_DIR)
	@echo "✓ Cleaned"

tidy:
	go mod tidy

vet:
	go vet ./...

fmt:
	gofmt -w .