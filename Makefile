VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test vet lint fmt install corpus clean
build:
	go build -ldflags "-s -w" -o oq ./cmd/oq

test:
	go test ./...

vet:
	go vet ./...

# needs: go install honnef.co/go/tools/cmd/staticcheck@latest
lint: vet
	test -z "$$(gofmt -s -l .)" || (gofmt -s -l . && exit 1)
	staticcheck ./...

fmt:
	gofmt -s -w .

# Per-user by default (no root). System-wide: sudo make install PREFIX=/usr/local
PREFIX ?= $(HOME)/.local

install: build
	install -d $(DESTDIR)$(PREFIX)/bin $(DESTDIR)$(PREFIX)/share/man/man1
	install -m 755 oq $(DESTDIR)$(PREFIX)/bin/oq
	install -m 644 man/oq.1 $(DESTDIR)$(PREFIX)/share/man/man1/oq.1

# Slice and round-trip checks over a directory of specs: make corpus DIR=path
corpus:
	OQ_CORPUS=$(DIR) go test ./internal/oq -run TestCorpus -v -timeout 60m

clean:
	rm -f oq
