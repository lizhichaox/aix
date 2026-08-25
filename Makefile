.PHONY: release build install vet test clean

# Show current version
version:
	@grep 'var Version' cmd/root.go | grep -o '"[^"]*"' | tr -d '"'

# Build only
build:
	go build -o aix .

# Install locally
install: build
	./install.sh

# Build and install locally (no version bump)
rebuild: build
	NO_SETUP=1 ./install.sh
	@echo "✓ rebuilt and live"


# Bump patch version, build, install, restart proxy
release:
	@V=$$(grep 'var Version' cmd/root.go | grep -o '"[^"]*"' | tr -d '"'); \
	N=$$(echo $$V | awk -F. '{printf "%d.%d.%d", $$1, $$2, $$3+1}'); \
	sed -i '' "s/var Version = \"$$V\"/var Version = \"$$N\"/" cmd/root.go 2>/dev/null || \
	sed -i "s/var Version = \"$$V\"/var Version = \"$$N\"/" cmd/root.go; \
	echo "Bumping $$V → $$N"; \
	go vet ./... && go build -o aix . && \
	NO_SETUP=1 ./install.sh && \
	echo "✓ v$$N released"

# Run tests
test:
	go test ./...

# Vet + build check (CI)
vet:
	go vet ./...
	go build ./...

# Clean build artifacts
clean:
	rm -f aix
