GO ?= go
BIN := shunt

.PHONY: all build test vet fmt lint run clean cross cross-linux cross-darwin cross-windows

all: build

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/shunt

test:
	CGO_ENABLED=0 $(GO) test ./... -count=1

race:
	CGO_ENABLED=1 $(GO) test -race ./... -count=1

vet:
	$(GO) vet ./...

fmt:
	gofmt -w ./cmd ./internal ./test

lint: fmt vet

run: build
	./$(BIN) serve

clean:
	rm -f $(BIN) $(BIN).exe shunt.exe shunt.exe~ coverage.out
	rm -rf dist

# Cross-compile every supported target into dist/. CGO is disabled so the
# output is portable and needs no C toolchain.
cross: cross-linux cross-darwin cross-windows

cross-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags="-s -w" -o dist/shunt-linux-amd64 ./cmd/shunt
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags="-s -w" -o dist/shunt-linux-arm64 ./cmd/shunt

cross-darwin:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GO) build -trimpath -ldflags="-s -w" -o dist/shunt-darwin-amd64 ./cmd/shunt
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -trimpath -ldflags="-s -w" -o dist/shunt-darwin-arm64 ./cmd/shunt

cross-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags="-s -w" -o dist/shunt-windows-amd64.exe ./cmd/shunt
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 $(GO) build -trimpath -ldflags="-s -w" -o dist/shunt-windows-arm64.exe ./cmd/shunt
