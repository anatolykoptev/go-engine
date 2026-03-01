.PHONY: lint test build clean

lint:
	golangci-lint run ./...

test:
	go test -race -count=1 ./...

build:
	go build ./...

clean:
	go clean -testcache
