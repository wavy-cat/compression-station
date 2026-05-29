CGO_ENABLED=0

.PHONY: build clean

all: build

build:
	go build -trimpath -ldflags="-s -w" -o server github.com/wavy-cat/compression-station/cmd/server

vet:
	go vet ./...

test:
	go test -v ./...

clean:
	rm -f server

clean-win:
	del .\server