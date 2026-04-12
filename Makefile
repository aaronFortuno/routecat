APP     := routecat
VERSION := 0.1.0
COMMIT  := $(shell git rev-parse --short HEAD)
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT)"

.PHONY: build build-linux run test vet clean

build:
	go build $(LDFLAGS) -o dist/$(APP) ./cmd/routecat

build-linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o dist/$(APP) ./cmd/routecat

run: build
	./dist/$(APP)

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf dist/
