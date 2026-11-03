.PHONY: build test check fmt

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
