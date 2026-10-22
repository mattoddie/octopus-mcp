VERSION ?= 0.0.0-dev
LDFLAGS := -s -w -X github.com/mattoddie/octopus-mcp/internal/tools.Version=$(VERSION)
export CGO_ENABLED := 0

# Platforms for the MCP Bundles (one .mcpb each), as GOOS/GOARCH.
MCPB_PLATFORMS := darwin/arm64 darwin/amd64 windows/amd64 windows/arm64 linux/amd64 linux/arm64

# The MCP Registry's publisher CLI, pinned with the SHA-256 of each release
# archive (from the release's assets). See docs/releasing.md.
MCP_PUBLISHER_VERSION := 1.8.1
MCP_PUBLISHER_SHA256_linux_amd64 := a06c9096dcb9727c13555b6be26c7effa707b01f06a4c561ba7a3635443cf2cc
MCP_PUBLISHER_SHA256_linux_arm64 := 8dd75a6cf6845688b5d4e46df58d3ca26d5c8d233bb0626606e1db82c5e883e4
MCP_PUBLISHER_SHA256_darwin_amd64 := 88126981225e7714fcc6b7a10cdba4a80ae5901e9740a8c06d0d5195c8bc294c
MCP_PUBLISHER_SHA256_darwin_arm64 := e45e520892460732a4bdf37255576415d4a53ec171f8b913faf15bb1aef7cb77
HOST_OS := $(shell uname -s | tr A-Z a-z)
HOST_ARCH := $(shell uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
MCP_PUBLISHER_DIR := .build/mcp-publisher-$(MCP_PUBLISHER_VERSION)
MCP_PUBLISHER := $(MCP_PUBLISHER_DIR)/mcp-publisher
SERVER_JSON ?= server.json

.PHONY: build test lint docs docs-check image mcpb server-json mcp-publisher registry-validate registry-publish clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/octopus-mcp ./cmd/octopus-mcp

test:
	go test ./...

lint:
	gofmt -l . | (! grep .) || (echo "Run gofmt -w ." && exit 1)
	go vet ./...

docs:
	go run ./cmd/gen-tool-docs
	go run ./cmd/mcpb manifest

docs-check:
	go run ./cmd/gen-tool-docs -check
	go run ./cmd/mcpb manifest -check

image:
	docker build --build-arg VERSION=$(VERSION) -t octopus-mcp:$(VERSION) .

# MCP Bundles for Claude Desktop: dist/octopus-mcp-$(VERSION)-<os>-<arch>.mcpb.
mcpb:
	rm -rf .build/mcpb dist/*.mcpb
	go build -o .build/mcpb/pack ./cmd/mcpb
	set -e; for p in $(MCPB_PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; \
		if [ "$$os" = windows ]; then ext=.exe; fi; \
		GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o .build/mcpb/$$os-$$arch/octopus-mcp$$ext ./cmd/octopus-mcp; \
		.build/mcpb/pack pack -version $(VERSION) -goos $$os -binary .build/mcpb/$$os-$$arch/octopus-mcp$$ext \
			-o dist/octopus-mcp-$(VERSION)-$$os-$$arch.mcpb; \
	done

# server.json for the MCP Registry, stamped with VERSION and the SHA-256 of each
# bundle in dist/, written to dist/server.json. Run it after `make mcpb`, or
# after downloading a release's bundles into dist/.
server-json:
	cd dist && sha256sum *.mcpb > SHA256SUMS
	jq --arg version '$(VERSION)' --rawfile sums dist/SHA256SUMS -f mcpb/server-json.jq server.json > dist/server.json.tmp
	mv dist/server.json.tmp dist/server.json
	@echo "Wrote dist/server.json for $(VERSION)."

mcp-publisher: $(MCP_PUBLISHER)

$(MCP_PUBLISHER):
	@sha='$(MCP_PUBLISHER_SHA256_$(HOST_OS)_$(HOST_ARCH))'; \
	if [ -z "$$sha" ]; then echo "No pinned mcp-publisher checksum for $(HOST_OS)/$(HOST_ARCH)."; exit 1; fi; \
	set -e; dir='$(MCP_PUBLISHER_DIR)'; mkdir -p "$$dir"; \
	curl -fsSL -o "$$dir/mcp-publisher.tar.gz" \
		'https://github.com/modelcontextprotocol/registry/releases/download/v$(MCP_PUBLISHER_VERSION)/mcp-publisher_$(HOST_OS)_$(HOST_ARCH).tar.gz'; \
	echo "$$sha  $$dir/mcp-publisher.tar.gz" | sha256sum -c -; \
	tar -xzf "$$dir/mcp-publisher.tar.gz" -C "$$dir" mcp-publisher; \
	rm "$$dir/mcp-publisher.tar.gz"

# Check server.json (or SERVER_JSON=dist/server.json) against the registry's
# schema and rules. This sends the file to the registry's validate endpoint; it
# doesn't log in or publish anything.
registry-validate: $(MCP_PUBLISHER)
	$(MCP_PUBLISHER) validate $(SERVER_JSON)

# Publish to the MCP Registry. The release workflow does this with
# REGISTRY_LOGIN=github-oidc; by hand, the default `github` login opens a
# browser sign-in. See docs/releasing.md.
REGISTRY_LOGIN ?= github
registry-publish: $(MCP_PUBLISHER)
	$(MCP_PUBLISHER) login $(REGISTRY_LOGIN)
	$(MCP_PUBLISHER) publish $(SERVER_JSON)

clean:
	rm -rf bin .build dist
