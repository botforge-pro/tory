STATICCHECK_VERSION := v0.8.1
STATICCHECK := $(shell go env GOPATH)/bin/staticcheck

# The Go that CI installs rather than whatever Homebrew last upgraded to: a
# newer local Go can write export data the pinned staticcheck cannot read, and
# lint then fails here only.
GOTOOLCHAIN ?= go1.27.1
export GOTOOLCHAIN

.PHONY: tools format lint test-build test build

# tory is a library: it has no run, install or deploy target.

tools:
	go install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)

format:
	gofmt -w .

lint:
	go vet ./...
	$(STATICCHECK) ./...

test-build:
	go build ./...
	go test -run '^$$' ./...

test:
	go test -count=1 ./...

build:
	go build ./...
