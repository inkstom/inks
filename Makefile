# inks — build, test and package the native macOS app.
#
#   make            build the binary (build/inks)
#   make run        build and run it
#   make test       go vet + unit tests
#   make selftest   end-to-end checks in a real window
#   make app        build/inks.app
#   make dmg        dist/inks_<version>_<arch>.dmg
#   make release    test, then app and dmg
#   make clean      remove build output

VERSION := $(shell cat VERSION)
LDFLAGS := -s -w -X main.version=$(VERSION) -extldflags=-Wl,-no_warn_duplicate_libraries
GOFLAGS := -trimpath

.PHONY: build run test selftest app dmg release clean

build:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o build/inks ./cmd/inks

run: build
	./build/inks

test:
	go vet ./...
	go test -ldflags=-extldflags=-Wl,-no_warn_duplicate_libraries ./...

# Opens a window, runs the scripted checks (editing, search, git, builds,
# plugins…) against a throwaway profile, and exits non-zero on failure.
selftest: build
	INKS_SELFTEST=1 INKS_NO_SESSION=1 INKS_USER_DATA=$$(mktemp -d) ./build/inks

app:
	sh scripts/package.sh

dmg: app
	sh scripts/make-dmg.sh

release: test dmg

clean:
	rm -rf build dist
