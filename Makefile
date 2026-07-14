PNPM ?= pnpm

.PHONY: build check format format-check generated-check license-check lint test

build:
	go build ./cmd/openbox ./cmd/openboxd
	$(PNPM) --dir web build

format:
	gofmt -w cmd internal
	$(PNPM) --dir web exec prettier --write .

format-check:
	test -z "$$(gofmt -l cmd internal)"

lint:
	go vet ./...
	$(PNPM) --dir web lint

test:
	go test ./...
	$(PNPM) --dir web test

license-check:
	./scripts/check-license-headers.sh

generated-check:
	./scripts/check-generated.sh

check: format-check lint test license-check generated-check build
