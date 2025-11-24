BINARY  := https-tunnel
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# Everything here delegates to build.sh, so the make and script paths cannot
# drift apart. build.sh is the one to read.
.PHONY: all web quick test vet dev examples cross run smoke clean

all:   ; @./build.sh all
web:   ; @./build.sh web
quick: ; @./build.sh quick
test:  ; @./build.sh test
vet:   ; @./build.sh vet
dev:   ; @./build.sh dev
examples: ; @./build.sh examples
cross: ; @./build.sh cross
run:   ; @./build.sh run
smoke: ; @./build.sh smoke
clean: ; @./build.sh clean
