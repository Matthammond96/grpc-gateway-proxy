APP_NAME := grpc-gateway-proxy
BIN_DIR := bin

.PHONY: all build clean

all: build

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(APP_NAME) .

clean:
	rm -rf $(BIN_DIR)