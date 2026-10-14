VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse HEAD 2>/dev/null)
LDFLAGS := -s -w -X main.version=$(VERSION)
DEV_LDFLAGS := $(LDFLAGS) -X main.commit=$(COMMIT)

# deploy-dev targets (developer desktops)
DEV_TARGETS := linux/amd64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: build build-dev dist test vet clean

# Server binary (static linux/amd64): copy it to the server and run
# `sudo ./deploy-tools server install`.
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/deploy-tools ./cmd/deploy-tools

# Developer binary for this machine. COMMIT is what `deploy-dev init` pins
# the deploy action to, so build it from a released tag.
build-dev:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(DEV_LDFLAGS)" -o bin/deploy-dev ./cmd/deploy-dev

# Every release asset into dist/, each with its .sha256.
dist:
	rm -rf dist && mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/deploy-tools-linux-amd64 ./cmd/deploy-tools
	@for t in $(DEV_TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		out=dist/deploy-dev-$$os-$$arch$$ext; \
		echo "build $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(DEV_LDFLAGS)" -o $$out ./cmd/deploy-dev || exit 1; \
	done
	cd dist && for f in *; do sha256sum "$$f" > "$$f.sha256"; done

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf bin dist
