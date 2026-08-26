.PHONY: generate check-generate fmt test test-integration test-e2e lint build

generate:
	go generate ./api ./client

check-generate:
	@tmp=$$(mktemp -d); trap 'rm -rf $$tmp' EXIT; \
	cp api/generated.go $$tmp/api-generated.go; \
	cp client/generated.go $$tmp/client-generated.go; \
	$(MAKE) generate; \
	diff -u $$tmp/api-generated.go api/generated.go; \
	diff -u $$tmp/client-generated.go client/generated.go

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

test:
	go test ./...

test-integration:
	go test -tags=integration ./...

test-e2e:
	./scripts/e2e-compose.sh

lint:
	npx --yes @redocly/cli@2.47.0 lint api/openapi.yaml
	go vet ./...

build:
	go build ./cmd/tlon
