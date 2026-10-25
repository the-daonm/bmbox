# bmbox builds for linux/amd64: its domains are x86_64 (q35 + OVMF).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG     := github.com/the-daonm/bmbox/cmd
LDFLAGS := -s -w -X $(PKG).version=$(VERSION) -X $(PKG).commit=$(COMMIT) -X $(PKG).date=$(DATE)
GO      ?= go
NFPM    := $(GO) run github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.47.0
STATICCHECK := $(GO) run honnef.co/go/tools/cmd/staticcheck@v0.8.2

.PHONY: build test lint e2e dist packages clean

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/bmbox .

test:
	$(GO) test ./...

lint:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	$(GO) vet ./...
	$(STATICCHECK) ./...

# On a libvirt host with sudo (see scripts/e2e.sh).
e2e: build
	BMBOX=bin/bmbox scripts/e2e.sh

# Release artifacts in dist/: a tarball, a .deb and an .rpm.
dist: build
	rm -rf dist && mkdir -p dist/bmbox-$(VERSION)-linux-amd64
	cp bin/bmbox README.md dist/bmbox-$(VERSION)-linux-amd64/
	cp -r examples dist/bmbox-$(VERSION)-linux-amd64/
	tar -C dist -czf dist/bmbox-$(VERSION)-linux-amd64.tar.gz bmbox-$(VERSION)-linux-amd64
	rm -rf dist/bmbox-$(VERSION)-linux-amd64
	$(MAKE) packages

packages:
	mkdir -p dist
	VERSION=$(VERSION) $(NFPM) package --config packaging/nfpm.yaml --packager deb --target dist/
	VERSION=$(VERSION) $(NFPM) package --config packaging/nfpm.yaml --packager rpm --target dist/
	cd dist && sha256sum * > SHA256SUMS

clean:
	rm -rf bin dist
