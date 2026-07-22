# wa-engine Makefile
# Usage:
#   make mac          - build server binary for macOS (current arch)
#   make linux        - cross-compile for linux/amd64
#   make windows      - cross-compile for windows/amd64
#   make run          - build + run locally on port 8080
#   make test-send    - quick smoke test (requires running server + jq)
#   make clean        - remove build artifacts

MODULE     := github.com/mml/wa-engine
SERVER_PKG := ./cmd/server
OUT_DIR    := ./build
DATA_DIR   := ./wa-data
VERSION    ?= dev
LD_FLAGS   := -ldflags="-s -w -X $(MODULE)/core.Version=$(VERSION)"

.PHONY: all mac linux windows run test-send clean

all: mac

# ─── Desktop / Server builds ──────────────────────────────────────────────────

mac:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=1 go build $(LD_FLAGS) -o $(OUT_DIR)/waengine ./cmd/server
	@echo "✓ Built $(OUT_DIR)/waengine (macOS)"

linux:
	@mkdir -p $(OUT_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 \
	  CC=x86_64-linux-musl-gcc \
	  go build $(LD_FLAGS) -o $(OUT_DIR)/waengine-linux ./cmd/server
	@echo "✓ Built $(OUT_DIR)/waengine-linux"

# Windows cross-compile requires mingw: brew install mingw-w64
windows:
	@mkdir -p $(OUT_DIR)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 \
	  CC=x86_64-w64-mingw32-gcc \
	  go build $(LD_FLAGS) -o $(OUT_DIR)/waengine.exe ./cmd/server
	@echo "✓ Built $(OUT_DIR)/waengine.exe"

# ─── Local run ────────────────────────────────────────────────────────────────

run: mac
	$(OUT_DIR)/waengine --port 8080 --data $(DATA_DIR)

# Run with an API key (more secure)
run-secure: mac
	$(OUT_DIR)/waengine --port 8080 --data $(DATA_DIR) --key my-secret-key

# Install as a background system service (auto-starts on boot, survives sleep/wake)
install-service: mac
	sudo $(OUT_DIR)/waengine --install-service --port 8080

uninstall-service:
	sudo $(OUT_DIR)/waengine --uninstall-service

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

# ─── Phone pairing test (requires running server + jq installed) ──────────────

test-phone-pair:
	@echo "=== Health check ==="
	curl -s http://localhost:8080/api/health | jq .
	@echo "\n=== List sessions ==="
	curl -s http://localhost:8080/api/sessions | jq .
	@echo "\n=== Start phone pairing (session: test, phone: PHONE_NUMBER) ==="
	@read -p "Enter phone number (with country code, no +): " phone; \
	curl -s -X POST http://localhost:8080/api/sessions/test/pair-code \
	  -H "Content-Type: application/json" \
	  -d "{\"phone\":\"$$phone\"}" | jq .
	@echo "\n=== Poll pairing code ==="
	@sleep 1
	curl -s http://localhost:8080/api/sessions/test/pairing-code | jq .
	@echo "\n=== Poll events (waiting for pairing.code) ==="
	curl -s http://localhost:8080/api/sessions/test/events | jq .

# ─── Maintenance ──────────────────────────────────────────────────────────────

# ─── Checksums ─────────────────────────────────────────────────────────────────
sha256sums:
	@for f in $(OUT_DIR)/waengine*; do \
	  [ -f "$$f" ] && sha256sum "$$f" | sed 's|$(OUT_DIR)/||'; \
	done > $(OUT_DIR)/SHA256SUMS
	@echo "✓ Generated $(OUT_DIR)/SHA256SUMS"
	@cat $(OUT_DIR)/SHA256SUMS

clean:
	rm -rf $(OUT_DIR)
	@echo "✓ Cleaned"

tidy:
	go mod tidy

vet:
	go vet ./...

fmt:
	gofmt -w .