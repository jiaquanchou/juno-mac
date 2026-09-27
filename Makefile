.PHONY: build test fmt vet clean

build:
	go build -o juno-mac ./cmd/juno-mac

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -f juno-mac
