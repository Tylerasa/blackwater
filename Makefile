.PHONY: test lint build analyse clean

GO ?= go

test:
	$(GO) test ./...

lint:
	$(GO) vet ./...
	@fmt=$$($(GO) fmt ./...); \
	if [ -n "$$fmt" ]; then \
		echo "gofmt needs to run on:"; echo "$$fmt"; exit 1; \
	fi

build:
	$(GO) build -o bin/ledger ./cmd/ledger

# usage: make analyse INPUT=/path/to/dump.xml
analyse: build
	@if [ -z "$(INPUT)" ]; then echo "set INPUT=/path/to/dump"; exit 1; fi
	./bin/ledger analyse --input $(INPUT) --all

clean:
	rm -rf bin/
