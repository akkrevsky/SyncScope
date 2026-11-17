GO ?= $(shell command -v go 2>/dev/null || { [ -x $(CURDIR)/../.toolchain/go/bin/go ] && echo $(CURDIR)/../.toolchain/go/bin/go; } || echo go)
GOCACHE ?= $(CURDIR)/.gocache
GOPATH ?= $(CURDIR)/.gopath
export GOCACHE
export GOPATH

.PHONY: all build test vet e2e fetch-syncthing fetch-strelaysrv relay-e2e clean

all: build

build:
	mkdir -p bin
	$(GO) build -o bin/syncscoped ./cmd/syncscoped
	$(GO) build -o bin/syncscope-origin ./cmd/syncscope-origin
	$(GO) build -o bin/syncscope-client ./cmd/syncscope-client
	$(GO) build -o bin/syncscope-login ./cmd/syncscope-login

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fetch-syncthing:
	./lab/fetch-syncthing.sh

e2e: build fetch-syncthing
	cd lab && python3 e2e.py

fetch-strelaysrv:
	./lab/fetch-strelaysrv.sh

relay-e2e: build fetch-syncthing fetch-strelaysrv
	cd lab && python3 relay-e2e.py

clean:
	rm -rf bin lab/bin lab/relay-bin lab/e2e-run lab/relay-run data
