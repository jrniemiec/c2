GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS ?= -X main.version=$(VERSION)
BINARY  := c2
INSTALL := $(HOME)/dev/bin/$(BINARY)

# Sherpa-onnx dylib locations (from Go module cache)
SHERPA_VERSION := v1.13.0
GOARCH_OS      := $(shell $(GO) env GOARCH)
ifeq ($(GOARCH_OS),arm64)
  SHERPA_ARCH := aarch64-apple-darwin
else
  SHERPA_ARCH := x86_64-apple-darwin
endif
SHERPA_LIB_DIR := $(shell $(GO) env GOPATH)/pkg/mod/github.com/k2-fsa/sherpa-onnx-go-macos@$(SHERPA_VERSION)/lib/$(SHERPA_ARCH)

# Homebrew llvm libs (needed for macOS < 14 compatibility)
LLVM_PREFIX  := $(shell brew --prefix llvm 2>/dev/null)
LLVM_LIBCXX  := $(LLVM_PREFIX)/lib/c++/libc++.1.dylib
LLVM_LIBCXXABI := $(LLVM_PREFIX)/lib/c++/libc++abi.1.dylib
LLVM_LIBUNWIND := $(LLVM_PREFIX)/lib/unwind/libunwind.1.dylib

DIST_NAME := $(BINARY)-v$(VERSION)-darwin-$(GOARCH_OS)
DIST_DIR  := dist/$(DIST_NAME)

.PHONY: build install run test fmt vet clean dist release

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
	rm -rf bin/ dist/

dist: build
	@if [ -z "$(SHERPA_LIB_DIR)" ] || [ ! -d "$(SHERPA_LIB_DIR)" ]; then \
		echo "error: sherpa-onnx lib dir not found: $(SHERPA_LIB_DIR)"; exit 1; \
	fi
	@mkdir -p $(DIST_DIR)/bin $(DIST_DIR)/lib
	@cp bin/$(BINARY) $(DIST_DIR)/bin/$(BINARY)
	@# Replace build-machine RPATH with portable @executable_path/../lib
	install_name_tool \
		-delete_rpath "$(SHERPA_LIB_DIR)" \
		-add_rpath "@executable_path/../lib" \
		$(DIST_DIR)/bin/$(BINARY)
	@# Copy dylibs
	cp $(SHERPA_LIB_DIR)/libsherpa-onnx-c-api.dylib $(DIST_DIR)/lib/
	cp $(SHERPA_LIB_DIR)/libonnxruntime.1.24.4.dylib $(DIST_DIR)/lib/
	@# Bundle Homebrew llvm libc++ chain so the binary works on macOS < 14
	@if [ -f "$(LLVM_LIBCXX)" ]; then \
		cp $(LLVM_LIBCXX) $(DIST_DIR)/lib/libc++.1.dylib; \
		cp $(LLVM_LIBCXXABI) $(DIST_DIR)/lib/libc++abi.1.dylib; \
		cp $(LLVM_LIBUNWIND) $(DIST_DIR)/lib/libunwind.1.dylib; \
		LLVM_ABS=$(shell brew --prefix llvm)/lib/c++; \
		LLVM_UNWIND=$(shell brew --prefix llvm)/lib/unwind; \
		for f in $(DIST_DIR)/lib/libc++.1.dylib $(DIST_DIR)/lib/libc++abi.1.dylib; do \
			install_name_tool \
				-id @rpath/$$(basename $$f) \
				-change $$LLVM_ABS/libc++.1.dylib    @rpath/libc++.1.dylib \
				-change $$LLVM_ABS/libc++abi.1.dylib @rpath/libc++abi.1.dylib \
				$$f; \
		done; \
		install_name_tool \
			-id @rpath/libunwind.1.dylib \
			-change $$LLVM_UNWIND/libunwind.1.dylib @rpath/libunwind.1.dylib \
			$(DIST_DIR)/lib/libunwind.1.dylib; \
		install_name_tool \
			-change /usr/lib/libc++.1.dylib @rpath/libc++.1.dylib \
			$(DIST_DIR)/lib/libonnxruntime.1.24.4.dylib; \
		install_name_tool \
			-change /usr/lib/libc++.1.dylib @rpath/libc++.1.dylib \
			$(DIST_DIR)/lib/libsherpa-onnx-c-api.dylib; \
		codesign --force --sign - $(DIST_DIR)/lib/libonnxruntime.1.24.4.dylib; \
		codesign --force --sign - $(DIST_DIR)/lib/libsherpa-onnx-c-api.dylib; \
		codesign --force --sign - $(DIST_DIR)/lib/libc++.1.dylib; \
		codesign --force --sign - $(DIST_DIR)/lib/libc++abi.1.dylib; \
		codesign --force --sign - $(DIST_DIR)/lib/libunwind.1.dylib; \
		echo "Bundled libc++ chain for macOS < 14 compatibility"; \
	else \
		echo "warning: llvm not found, skipping libc++ bundle (macOS 14+ only)"; \
	fi
	@# Re-sign binary after modification
	codesign --force --sign - $(DIST_DIR)/bin/$(BINARY)
	@# Create tarball
	cd dist && tar czf $(DIST_NAME).tar.gz $(DIST_NAME)/
	@echo "Created: dist/$(DIST_NAME).tar.gz"

release:
	@if [ -z "$(VERSION)" ]; then echo "usage: make release VERSION=x.y.z"; exit 1; fi
	scripts/release-c2.sh $(VERSION)
