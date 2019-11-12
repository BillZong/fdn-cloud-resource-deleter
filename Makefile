# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
# Binary paramters
BINARY_PATH=bin
BINARY_NAME=ali-ecs-deleter
BINARY_DARWIN_AMD64=$(BINARY_PATH)/darwin/amd64/$(BINARY_NAME)
BINARY_LINUX_ARM64=$(BINARY_PATH)/linux/arm64/$(BINARY_NAME)
BINARY_ARCHVIE_PATH=archive
OUTPUT_BIN=output

all: zip $(OUTPUT_BIN)
	cp $(BINARY_ARCHVIE_PATH)/$(BINARY_NAME).tar.gz $(OUTPUT_BIN)
	cp delete-k8s.sh $(OUTPUT_BIN)
zip: build $(BINARY_ARCHVIE_PATH)
	tar -zcvf $(BINARY_ARCHVIE_PATH)/$(BINARY_NAME).tar.gz bin
$(BINARY_ARCHVIE_PATH):
	mkdir -p $(BINARY_ARCHVIE_PATH)
$(OUTPUT_BIN):
	mkdir -p $(OUTPUT_BIN)
#build: build-mac build-linux
build: build-linux
test: 
	$(GOTEST) -v ./...
cleanZip:
	rm -rf $(BINARY_PATH)/*
	rm -rf $(BINARY_ARCHVIE_PATH)/*
clean: cleanZip
	$(GOCLEAN)
	rm -rf $(OUTPUT_BIN)
run:
	$(GOBUILD) -o $(BINARY_PATH)/$(BINARY_NAME) -v ./...
	chmod +x $(BINARY_PATH)/$(BINARY_NAME)
	./$(BINARY_PATH)/$(BINARY_NAME) -h


# Cross compilation
build-mac: build-mac-amd64
build-mac-amd64:
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 $(GOBUILD) -o $(BINARY_DARWIN_AMD64) -v
build-linux: build-linux-arm64
build-linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GOBUILD) -o $(BINARY_LINUX_ARM64) -v
# docker-build:
	#docker run --rm -it -v "$(GOPATH)":/go -w /go/src/bitbucket.org/rsohlich/makepost golang:latest go build -o "$(BINARY_UNIX)" -v
