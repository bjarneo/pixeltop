# Build, test and package pixeltop. Set GO to use another Go command, for
# example: make release GO="mise exec go@1.27.1 -- go"
GO      ?= go
VERSION ?= 0.1.0
LDFLAGS := -s -w -X main.version=$(VERSION)
NAME    := pixeltop-$(VERSION)-linux-amd64

.PHONY: build test install release clean

build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o pixeltop .

test:
	$(GO) test ./...

install: build
	install -Dm755 pixeltop $(HOME)/.local/bin/pixeltop

# The release archive holds a static Linux amd64 binary and the README.
# Files in the archive belong to user 0, so no local user name goes in.
release: test
	rm -rf dist
	mkdir -p dist/$(NAME)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(NAME)/pixeltop .
	cp README.md dist/$(NAME)/
	tar -C dist --owner=0 --group=0 --numeric-owner --sort=name -czf dist/$(NAME).tar.gz $(NAME)
	cd dist && sha256sum $(NAME).tar.gz > SHA256SUMS

clean:
	rm -rf dist pixeltop
