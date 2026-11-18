.PHONY: build test race vet fmt check e2e

build:
	go build -o bin/caffe ./cmd/caffe

fmt:
	gofmt -l -w .

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

# Everything that must pass before a change is done.
check: vet race
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)

# Real agents through a real Herdr. Costs model tokens; not part of `check`.
# Needs a running Herdr session, see docs/development.md.
e2e:
	CAFFE_E2E=1 go test -count=1 -v -timeout 20m ./tests/integration/
