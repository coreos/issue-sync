export GO15VENDOREXPERIMENT=1
export CGO_ENABLED:=0

PROJ=issue-sync
ORG_PATH=github.com/coreos
REPO_PATH=$(ORG_PATH)/$(PROJ)
VERSION=$(shell ./git-version)
BUILD_TIME=`date +%FT%T%z`
GOOS=$(shell go env GOOS)
GOARCH=$(shell go env GOARCH)
SOURCES := $(shell find . -name '*.go')
LD_FLAGS=-ldflags "-X $(REPO_PATH)/cmd.Version=$(VERSION)"

build: bin/$(PROJ)

bin/$(PROJ): $(SOURCES)
	@go build -o bin/$(PROJ) $(LD_FLAGS) $(REPO_PATH)

clean:
	@rm bin/*

.PHONY: clean

.DEFAULT_GOAL: build
