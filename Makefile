# Load environment variables for proto submodule
GIT_SUBMODULE_FLAGS ?=

.PHONY: build clean proto proto-clean clean-all

# Default target
all: proto
	cargo build --release

# Clean build artifacts
clean:
	cargo clean

# Clean everything including source proto files
clean-all: clean
	@echo "Cleaning proto submodule..."
	@git $(GIT_SUBMODULE_FLAGS) submodule deinit -f proto/service 2>/dev/null || true
	@rm -rf proto/service

# Proto management
proto: proto/service/portfoliodb.proto

proto/service/portfoliodb.proto:
	@echo "Initializing and updating proto submodule..."
	@mkdir -p proto
	@git $(GIT_SUBMODULE_FLAGS) submodule update --init --recursive proto/service