VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOFLAGS  = -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: build test vet fmt-check check dist
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -o moneroid ./cmd/moneroid
test:
	go test -race ./...
vet:
	go vet ./...
fmt-check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt: files need formatting"; exit 1; }
check: fmt-check vet test build

# Release assets in dist/ (byte-reproducible): the binary, the extras tarball that
# install.sh unpacks (units, examples, docs, release signing keys) and SHA256SUMS.
EXTRAS = systemd examples keys docs/install.md docs/troubleshooting.md docs/updating.md
dist: build
	mkdir -p dist
	cp moneroid dist/moneroid-$(VERSION)-linux-amd64
	git ls-files $(EXTRAS) | tar --sort=name --owner=0 --group=0 --numeric-owner --mode=go-w \
		--mtime="$$(git log -1 --format=%cI)" --no-recursion -T - -cf - | gzip -9n > dist/moneroid-$(VERSION)-extras.tar.gz
	cd dist && sha256sum moneroid-$(VERSION)-linux-amd64 moneroid-$(VERSION)-extras.tar.gz > SHA256SUMS
