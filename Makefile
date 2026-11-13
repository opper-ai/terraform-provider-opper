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

# Local checks only; no external publishing or live credentials.
release-check:
	$(GORELEASER) check
	python3 scripts/check-release-notes.py "$(GORELEASER)"
	$(GORELEASER) release --snapshot --clean --skip=sign
	python3 scripts/check-release.py dist
