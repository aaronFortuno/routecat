APP     := routecat
VERSION := 0.1.0

.PHONY: build run test vet clean

build:
	go build -o dist/$(APP) ./cmd/routecat

run: build
	./dist/$(APP)

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf dist/
