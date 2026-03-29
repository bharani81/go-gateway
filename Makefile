.PHONY: all run build test tidy clean

APP_NAME = gateway
MAIN_FILE = cmd/gateway/main.go
CONFIG_FILE = configs/gateway.yaml

all: build

run:
	go run $(MAIN_FILE) -config=$(CONFIG_FILE)

build:
	go build -o bin/$(APP_NAME) $(MAIN_FILE)

test:
	go test -v -race ./...

stress:
	docker-compose -f deployments/docker-compose.stress.yml up --build --abort-on-container-exit
	docker-compose -f deployments/docker-compose.stress.yml down -v

tidy:
	go mod tidy

clean:
	rm -rf bin/
