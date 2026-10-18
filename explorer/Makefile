# Storage explorer (Wails v2 + Nuxt 3). See README.md.
WAILS_VERSION := v2.16.0
WAILS := .bin/wails
TAGS := webkit2_41
# A hash of the C++ archives the cgo wrappers link, stamped into every link: Go does not track the archives'
# contents, so without it a rebuilt systems/* library is not relinked (same trick as server/Makefile).
LIBS_HASH = $$(cat ../systems/stu/build/*.a ../systems/content/build/*.a ../systems/maps/build/*.a 2>/dev/null | md5sum | cut -c1-12)

.PHONY: all tools libs build dev test run clean

all: build

$(WAILS):
	GOBIN=$(CURDIR)/.bin go install github.com/wailsapp/wails/v2/cmd/wails@$(WAILS_VERSION)
tools: $(WAILS)

# The cgo wrappers link the static C++ libraries from their build directories.
libs:
	cmake -S ../systems/stu -B ../systems/stu/build -G Ninja && cmake --build ../systems/stu/build
	cmake -S ../systems/content -B ../systems/content/build -G Ninja && cmake --build ../systems/content/build
	cmake -S ../systems/maps -B ../systems/maps/build -G Ninja && cmake --build ../systems/maps/build

build: $(WAILS)
	$(WAILS) build -tags $(TAGS) -ldflags "-X main.libs=$(LIBS_HASH)"

dev: $(WAILS)
	$(WAILS) dev -tags $(TAGS)

test:
	go test -count=1 -tags $(TAGS) -ldflags "-X main.libs=$(LIBS_HASH)" . ./internal/...

run: build
	./build/bin/owexplorer

clean:
	rm -rf build/bin frontend/dist frontend/.nuxt frontend/.output
