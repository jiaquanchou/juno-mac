.PHONY: build test fmt vet clean matrix matrix-check

build:
	go build -o juno-mac ./cmd/juno-mac

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

matrix:
	go run ./tools/gen-rules-matrix

matrix-check:
	go run ./tools/gen-rules-matrix --check

clean:
	rm -f juno-mac
