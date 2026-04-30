GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS ?= -X main.version=$(VERSION)
BINARY  := c2
INSTALL := $(HOME)/dev/bin/$(BINARY)

.PHONY: build install test fmt vet clean

build:
	@mkdir -p bin
	$(GO) build -ldflags '$(LDFLAGS)' -o bin/$(BINARY) .

install: test build
	cp bin/$(BINARY) $(INSTALL)
	@echo "Installed: $(INSTALL)"

test:
	$(GO) test ./...

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

clean:
	rm -rf bin/
