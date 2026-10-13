MAKEFLAGS := -j2
SHELL     := /bin/bash

PROVIDER     := namespace
BINARY       := pulumi-resource-$(PROVIDER)
VERSION      ?= $(shell cat version.txt)
PLUGIN_DIR   := $(HOME)/.pulumi/plugins/resource-$(PROVIDER)-v$(VERSION)
PROVIDER_DIR := provider
VERSION_PKG  := github.com/pulumi/autoregistry/lib/providers/namespace/provider/pkg/version

TESTPARALLELISM := 4
WORKING_DIR     := $(shell pwd)
GO              ?= go

.PHONY: all tidy build install gen-sdk test lint clean fmt

all: build

## tidy: resolve Go modules (run first on a fresh checkout)
tidy:
	cd $(PROVIDER_DIR) && $(GO) mod tidy

## build: compile the provider binary into ./bin/
build:
	mkdir -p bin
	cd $(PROVIDER_DIR) && $(GO) build \
		-ldflags "-X $(VERSION_PKG).Version=$(VERSION)" \
		-o ../bin/$(BINARY) \
		./cmd/pulumi-resource-$(PROVIDER)

## install: install the binary into the Pulumi plugin directory
install: build
	mkdir -p $(PLUGIN_DIR)
	cp bin/$(BINARY) $(PLUGIN_DIR)/$(BINARY)

## gen-sdk: generate language SDKs from the compiled provider schema
gen-sdk: install
	pulumi package gen-sdk $(PLUGIN_DIR)/$(BINARY) --language typescript --out sdk/nodejs
	pulumi package gen-sdk $(PLUGIN_DIR)/$(BINARY) --language python    --out sdk/python
	pulumi package gen-sdk $(PLUGIN_DIR)/$(BINARY) --language go        --out sdk/go
	pulumi package gen-sdk $(PLUGIN_DIR)/$(BINARY) --language dotnet    --out sdk/dotnet
	pulumi package gen-sdk $(PLUGIN_DIR)/$(BINARY) --language java      --out sdk/java

## test: run Go unit tests
test:
	cd $(PROVIDER_DIR) && $(GO) test ./... -v -count=1 -timeout 10m -parallel $(TESTPARALLELISM)

## fmt: format Go source files
fmt:
	find $(PROVIDER_DIR) -name "*.go" | xargs gofmt -w

## lint: vet Go source files
lint:
	cd $(PROVIDER_DIR) && $(GO) vet ./...

## clean: remove build artifacts
clean:
	rm -rf bin/ sdk/
