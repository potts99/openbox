PNPM ?= pnpm

.PHONY: build check format format-check generated-check license-check lint test

build:
	go build ./cmd/openbox ./cmd/openboxd
	$(PNPM) --filter @openbox/web build

format:
	gofmt -w cmd internal pkg
	$(PNPM) --filter @openbox/web exec prettier --write .

format-check:
	test -z "$$(gofmt -l cmd internal pkg)"

lint:
	go vet ./...
	$(PNPM) --filter @openbox/web lint

test:
	go test ./...
	$(PNPM) --filter @openbox/web test

license-check:
	./scripts/check-license-headers.sh

generated-check:
	./scripts/check-generated.sh

check: format-check lint test license-check generated-check build
