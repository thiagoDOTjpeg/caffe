.PHONY: build test race vet fmt check e2e windows-kit

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X caffe/internal/app.Version=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" -o bin/caffe ./cmd/caffe

fmt:
	gofmt -l -w .

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

check: vet race
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)

e2e:
	CAFFE_E2E=1 go test -count=1 -v -timeout 20m ./tests/integration/

KIT := dist/windows
windows-kit:
	rm -rf dist/windows dist/linux dist/caffe-windows-kit.zip
	mkdir -p $(KIT)/tests $(KIT)/plans dist/linux
	GOOS=windows GOARCH=amd64 go build -o $(KIT)/caffe.exe ./cmd/caffe
	cd spikes/tui && GOOS=windows GOARCH=amd64 go build -o ../../$(KIT)/tui-spike.exe . && go build -o ../../dist/linux/tui-spike .
	cd spikes/pty && GOOS=windows GOARCH=amd64 go build -o ../../$(KIT)/pty-spike.exe .
	for p in $$(go list ./internal/... ./tests/...); do \
		n=$$(echo $$p | sed -e 's#^caffe/internal/##' -e 's#^caffe/##' -e 's#/#-#g'); \
		mkdir -p $(KIT)/tests/$$n; \
		GOOS=windows GOARCH=amd64 go test -c -o $(KIT)/tests/$$n/$$n.test.exe $$p; \
		[ -f $(KIT)/tests/$$n/$$n.test.exe ] || rmdir $(KIT)/tests/$$n; \
	done
	cp -r internal/app/testdata $(KIT)/tests/app/
	cp scripts/windows/run-tests.ps1 scripts/windows/README.md $(KIT)/
	cp scripts/windows/plans/*.json $(KIT)/plans/
	cd dist && zip -qr caffe-windows-kit.zip windows
	@echo "kit: dist/caffe-windows-kit.zip"
