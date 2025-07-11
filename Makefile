.PHONY: build clean proto proto-clean clean-all

# Default target
all: proto
	cargo build --release

# Clean build artifacts
clean:
	cargo clean

# Clean everything including source proto files
clean-all: clean
	@rm -rf proto/service

# Proto management
proto: proto/service/portfoliodb.proto

proto/service/portfoliodb.proto:
	@echo "Proto repository not found, checking out https://github.com/leedenison/portfoliodb-proto"
	@mkdir -p proto
	@git clone https://github.com/leedenison/portfoliodb-proto proto/service