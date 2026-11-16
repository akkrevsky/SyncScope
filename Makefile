GO ?= go
GOCACHE ?= $(CURDIR)/.gocache
GOPATH ?= $(CURDIR)/.gopath
export GOCACHE
export GOPATH

.PHONY: all build test vet e2e fetch-syncthing clean

all: build

build:
	mkdir -p bin
	$(GO) build -o bin/syncscoped ./cmd/syncscoped
	$(GO) build -o bin/syncscope-origin ./cmd/syncscope-origin
	$(GO) build -o bin/syncscope-client ./cmd/syncscope-client

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fetch-syncthing:
	./lab/fetch-syncthing.sh

e2e: build fetch-syncthing
	cd lab && python3 e2e.py

clean:
	rm -rf bin lab/bin lab/e2e-run data
