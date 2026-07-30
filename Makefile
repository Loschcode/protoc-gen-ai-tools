.PHONY: build install clean

build:
	go build -o bin/protoc-gen-ai-tools ./cmd/protoc-gen-ai-tools

install:
	go install ./cmd/protoc-gen-ai-tools

clean:
	rm -rf bin/
