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

SHARE = $(DESTDIR)$(PREFIX)/share

# zsh reads completions only from $fpath, which rarely includes the per-user
# share directory. Unless DESTDIR is set (packaging), install also copies _oq
# into the first writable completions directory on the user's fpath.

install: build
	install -d $(DESTDIR)$(PREFIX)/bin $(SHARE)/man/man1 $(SHARE)/bash-completion/completions \
		$(SHARE)/zsh/site-functions $(SHARE)/fish/vendor_completions.d
	install -m 755 oq $(DESTDIR)$(PREFIX)/bin/oq
	install -m 644 man/oq.1 $(SHARE)/man/man1/oq.1
	./oq completion bash > $(SHARE)/bash-completion/completions/oq
	./oq completion zsh > $(SHARE)/zsh/site-functions/_oq
	./oq completion fish > $(SHARE)/fish/vendor_completions.d/oq.fish
	@[ -n "$(DESTDIR)" ] || ! command -v zsh >/dev/null || { \
		fp=$$(zsh -ic 'print -l $$fpath' 2>/dev/null); \
		echo "$$fp" | grep -qx '$(PREFIX)/share/zsh/site-functions' || \
		for d in $$fp; do case $$d in *site-functions|*completions) \
			if [ -d "$$d" ] && [ -w "$$d" ]; then \
				install -m 644 $(SHARE)/zsh/site-functions/_oq "$$d/_oq" && echo "zsh completion: $$d/_oq"; break; \
			fi;; esac; done; }

# Slice and round-trip checks over a directory of specs: make corpus DIR=path
corpus:
	OQ_CORPUS=$(DIR) go test ./internal/oq -run TestCorpus -v -timeout 60m

clean:
	rm -f oq
