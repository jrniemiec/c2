GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS ?= -X main.version=$(VERSION)
BINARY  := c2
INSTALL := $(HOME)/dev/bin/$(BINARY)

.PHONY: build install run test fmt vet clean

build:
	@mkdir -p bin
	$(GO) build -ldflags '$(LDFLAGS)' -o bin/$(BINARY) .

install: test build
	cp bin/$(BINARY) $(INSTALL)
	codesign --force --sign - $(INSTALL)
	@echo "Installed: $(INSTALL)"

run: build
	bin/$(BINARY)

test:
	$(GO) test ./...

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

clean:
	rm -rf bin/
