.PHONY: build run test clean docker docker-run docker-stop

# 默认后端地址
DEFAULT_BACKEND ?= https://open.bigmodel.cn/api/anthropic

build:
	CGO_ENABLED=0 go build -o bin/mcc ./cmd/server

run:
	go run ./cmd/server

test:
	go test -v -race -coverprofile=coverage.out ./...

clean:
	rm -rf bin/ coverage.out

docker:
	docker build -t magic-claude-code .

docker-run:
	docker compose up -d

docker-stop:
	docker compose down
