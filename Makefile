GO ?= go
DIST_DIR ?= dist
VERSION ?=
LDFLAGS ?= -s -w

.PHONY: generate build build-linux build-linux-amd64 build-linux-arm64 package-extension clean tag

generate:
	protoc --go_out=. --go_opt=paths=source_relative \
		--connect-go_out=. --connect-go_opt=paths=source_relative \
		api/logsearch/v1/logsearch.proto

build:
	$(GO) build ./cmd/logsearch-agent ./cmd/logsearch-cli

build-linux: build-linux-amd64 build-linux-arm64

build-linux-amd64:
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags="$(LDFLAGS)" \
		-o $(DIST_DIR)/logsearch-agent-linux-amd64 ./cmd/logsearch-agent
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags="$(LDFLAGS)" \
		-o $(DIST_DIR)/logsearch-cli-linux-amd64 ./cmd/logsearch-cli

build-linux-arm64:
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags="$(LDFLAGS)" \
		-o $(DIST_DIR)/logsearch-agent-linux-arm64 ./cmd/logsearch-agent
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags="$(LDFLAGS)" \
		-o $(DIST_DIR)/logsearch-cli-linux-arm64 ./cmd/logsearch-cli

package-extension:
	mkdir -p $(DIST_DIR)
	rm -f $(DIST_DIR)/logsearch-extension.zip
	cd extension && zip -r ../$(DIST_DIR)/logsearch-extension.zip . -x '*.DS_Store'

clean:
	rm -rf $(DIST_DIR)

# 用法：make tag VERSION=v0.1.0
# 推送 v* 标签后，GitHub Actions 会发布二进制并构建多架构镜像。
tag:
	@test -n "$(VERSION)" || (echo "VERSION is required, example: make tag VERSION=v0.1.0" && exit 1)
	@case "$(VERSION)" in v[0-9]*) ;; *) echo "VERSION must start with v, example: v0.1.0"; exit 1 ;; esac
	@git rev-parse --is-inside-work-tree >/dev/null 2>&1 || (echo "current directory is not a git repository" && exit 1)
	@test -z "$$(git status --porcelain)" || (echo "working tree has uncommitted changes" && exit 1)
	@if git rev-parse "$(VERSION)" >/dev/null 2>&1; then echo "tag $(VERSION) already exists"; exit 1; fi
	git tag -a "$(VERSION)" -m "Release $(VERSION)"
	git push origin "$(VERSION)"
