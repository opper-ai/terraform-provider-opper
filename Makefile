.PHONY: build test check fmt release-check

GORELEASER ?= goreleaser

build:
	go build -o bin/terraform-provider-opper .

test:
	go test -race ./...

check:
	test -z "$$(gofmt -l main.go internal)"
	terraform fmt -check -recursive examples
	go vet ./...
	go test -race ./...
	go build -o bin/terraform-provider-opper .

fmt:
	gofmt -w main.go internal
	terraform fmt -recursive examples

# Unsigned local packages only; never uploads or publishes a release.
release-check:
	$(GORELEASER) check
	$(GORELEASER) release --snapshot --clean --skip=sign
	python3 scripts/check-release.py dist
