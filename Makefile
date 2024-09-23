BINARY = logportal

all: build

build:
	GOARCH=amd64 go build -o $(BINARY) main.go
	

.PHONY: all build