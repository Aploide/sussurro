APP_NAME := sussurro
BUILD_DIR := bin
CMD_DIR := cmd/sussurro

# Whisper.cpp configuration
WHISPER_DIR := third_party/whisper.cpp
WHISPER_INCLUDE := $(abspath $(WHISPER_DIR)/include)
WHISPER_GGML_INCLUDE := $(abspath $(WHISPER_DIR)/ggml/include)
C_INCLUDE_PATH := $(WHISPER_INCLUDE):$(WHISPER_GGML_INCLUDE)
LIBRARY_PATH := $(abspath $(WHISPER_DIR))

# go-llama.cpp configuration
LLAMA_DIR := third_party/go-llama.cpp

# Detect number of CPU cores for parallel builds
NPROCS := $(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 1)

# Detect OS and architecture for platform-specific builds
UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)
ifeq ($(UNAME_S),Darwin)
	BUILD_TYPE := metal
	GGML_METAL_PATH := -L$(WHISPER_DIR)/build/ggml/src/ggml-metal
else
	BUILD_TYPE :=
	GGML_METAL_PATH :=
endif

# Conservative CPU target for Apple Silicon.
# -mcpu=apple-m1 is the ARMv8.5-A baseline shared by all M-series chips (M1/M2/M3/M4).
# Without this, building on an M2+ machine can emit instructions (e.g. SME/AMX2)
# that trigger Illegal Instruction crashes on M1 hardware.
ARM_COMPAT_CFLAGS :=
ifeq ($(UNAME_S),Darwin)
ifeq ($(UNAME_M),arm64)
	ARM_COMPAT_CFLAGS := -mcpu=apple-m1
endif
endif

# ---- UI / overlay dependencies (Linux only) ----
HAS_LAYER_SHELL    := $(shell pkg-config --exists gtk-layer-shell          2>/dev/null && echo yes || echo no)

LAYER_CFLAGS  := $(shell pkg-config --cflags gtk+-3.0 2>/dev/null)
LAYER_LDFLAGS := $(shell pkg-config --libs   gtk+-3.0 2>/dev/null)

ifeq ($(HAS_LAYER_SHELL),yes)
LAYER_CFLAGS  += $(shell pkg-config --cflags gtk-layer-shell 2>/dev/null) -DHAVE_GTK_LAYER_SHELL
LAYER_LDFLAGS += $(shell pkg-config --libs   gtk-layer-shell 2>/dev/null)
endif

WV_CFLAGS  := $(shell pkg-config --cflags webkit2gtk-4.1 2>/dev/null || pkg-config --cflags webkit2gtk-4.0 2>/dev/null)
WV_LDFLAGS := $(shell pkg-config --libs   webkit2gtk-4.1 2>/dev/null || pkg-config --libs   webkit2gtk-4.0 2>/dev/null)

# If only webkit2gtk-4.1 is available, create a compat .pc file so that
# webview_go (which hardcodes pkg-config: webkit2gtk-4.0) can find it.
HAS_WV40 := $(shell pkg-config --exists webkit2gtk-4.0 2>/dev/null && echo yes || echo no)
HAS_WV41 := $(shell pkg-config --exists webkit2gtk-4.1 2>/dev/null && echo yes || echo no)

ifeq ($(HAS_WV40),no)
ifeq ($(HAS_WV41),yes)
COMPAT_PC_DIR := $(abspath .build-compat/pkgconfig)
PKG_CONFIG_PATH_UI := $(COMPAT_PC_DIR)$(if $(PKG_CONFIG_PATH),:$(PKG_CONFIG_PATH),)
else
$(warning Neither webkit2gtk-4.0 nor webkit2gtk-4.1 found; UI build will fail)
COMPAT_PC_DIR :=
PKG_CONFIG_PATH_UI :=
endif
else
COMPAT_PC_DIR :=
PKG_CONFIG_PATH_UI := $(PKG_CONFIG_PATH)
endif

# Base CGO link flags (whisper + llama)
BASE_LDFLAGS := -L$(WHISPER_DIR)/build/src -L$(WHISPER_DIR)/build/ggml/src \
	-L$(WHISPER_DIR)/build/ggml/src/ggml-cpu $(GGML_METAL_PATH) \
	-L$(WHISPER_DIR)/build/ggml/src/ggml-blas -lwhisper

# Export environment variables for CGO
export C_INCLUDE_PATH
export LIBRARY_PATH

VERSION := $(shell grep 'Version = ' internal/version/version.go 2>/dev/null \
	| sed 's/.*"\(.*\)"/\1/' | tr -d '[:space:]')

.PHONY: all build compat-pc run clean deps app icons package

all: build build-transcribe

deps:
	@mkdir -p third_party
	@if [ ! -d "$(WHISPER_DIR)" ]; then \
		echo "Cloning whisper.cpp..."; \
		git clone https://github.com/ggerganov/whisper.cpp.git $(WHISPER_DIR); \
		echo "Patching whisper.cpp symbols..."; \
		chmod +x scripts/patch-whisper.sh; \
		./scripts/patch-whisper.sh; \
	fi
	@echo "Building whisper.cpp library..."
	@cmake -S $(WHISPER_DIR) -B $(WHISPER_DIR)/build \
		-DGGML_NATIVE=OFF \
		-DBUILD_SHARED_LIBS=OFF \
		-DWHISPER_BUILD_TESTS=OFF \
		-DWHISPER_BUILD_EXAMPLES=OFF \
		$(if $(ARM_COMPAT_CFLAGS),-DCMAKE_C_FLAGS="$(ARM_COMPAT_CFLAGS)" -DCMAKE_CXX_FLAGS="$(ARM_COMPAT_CFLAGS)")
	@cmake --build $(WHISPER_DIR)/build --config Release --target whisper -j $(NPROCS)
	@if [ ! -d "$(LLAMA_DIR)" ]; then \
		echo "Cloning go-llama.cpp..."; \
		git clone --recursive https://github.com/AshkanYarmoradi/go-llama.cpp $(LLAMA_DIR); \
	fi
	@echo "Building go-llama.cpp library..."
	@$(MAKE) -C $(LLAMA_DIR) clean
	@$(MAKE) -j $(NPROCS) -C $(LLAMA_DIR) libbinding.a BUILD_TYPE=$(BUILD_TYPE)

# Create webkit2gtk-4.0 compatibility .pc when only 4.1 is installed
compat-pc:
ifneq ($(COMPAT_PC_DIR),)
	@mkdir -p $(COMPAT_PC_DIR)
	@printf 'Name: webkit2gtk-4.0\nDescription: WebKit2 GTK+ (4.1 compat)\nVersion: 2.99.0\nRequires: webkit2gtk-4.1\nLibs: %s\nCflags: %s\n' \
		"$(shell pkg-config --libs webkit2gtk-4.1)" \
		"$(shell pkg-config --cflags webkit2gtk-4.1)" \
		> $(COMPAT_PC_DIR)/webkit2gtk-4.0.pc
	@echo "  Created compat .pc: $(COMPAT_PC_DIR)/webkit2gtk-4.0.pc"
endif

# Build with full UI (overlay + tray + settings window)
build: deps compat-pc
	@echo "Building $(APP_NAME)..."
	@mkdir -p $(BUILD_DIR)
ifeq ($(UNAME_S),Darwin)
	CGO_LDFLAGS="$(BASE_LDFLAGS) -framework Cocoa -framework QuartzCore -framework CoreVideo -framework Foundation" \
	go build -o $(BUILD_DIR)/$(APP_NAME) ./$(CMD_DIR)
else
	@echo "  Layer shell  : $(HAS_LAYER_SHELL)"
	PKG_CONFIG_PATH="$(PKG_CONFIG_PATH_UI)" \
	CGO_CFLAGS="$(LAYER_CFLAGS) $(WV_CFLAGS)" \
	CGO_LDFLAGS="$(BASE_LDFLAGS) $(LAYER_LDFLAGS) $(WV_LDFLAGS)" \
	go build -o $(BUILD_DIR)/$(APP_NAME) ./$(CMD_DIR)
endif

# Build sussurro-transcribe CLI (no UI dependencies)
build-transcribe: deps
	@echo "Building sussurro-transcribe..."
	@mkdir -p $(BUILD_DIR)
ifeq ($(UNAME_S),Darwin)
	CGO_LDFLAGS="$(BASE_LDFLAGS) -framework Accelerate -framework Foundation" \
	go build -o $(BUILD_DIR)/sussurro-transcribe ./cmd/transcribe
else
	CGO_LDFLAGS="$(BASE_LDFLAGS)" \
	go build -o $(BUILD_DIR)/sussurro-transcribe ./cmd/transcribe
endif

run: build
	@echo "Running $(APP_NAME)..."
	@./$(BUILD_DIR)/$(APP_NAME)

# Generate platform icons (Sussurro.icns + hicolor PNG set) from
# internal/ui/assets/Logo.jpeg. Output lands in release/icons/.
icons:
	@chmod +x scripts/generate-icons.sh
	@./scripts/generate-icons.sh

# Build a Sussurro.app bundle in $(BUILD_DIR)/Sussurro.app on macOS.
# On Linux this target is a no-op (use `make package` for the
# .desktop entry + icon set instead).
app: build icons
ifeq ($(UNAME_S),Darwin)
	@echo "Bundling Sussurro.app (v$(VERSION))..."
	@rm -rf $(BUILD_DIR)/Sussurro.app
	@mkdir -p $(BUILD_DIR)/Sussurro.app/Contents/MacOS
	@mkdir -p $(BUILD_DIR)/Sussurro.app/Contents/Resources
	@cp $(BUILD_DIR)/$(APP_NAME) $(BUILD_DIR)/Sussurro.app/Contents/MacOS/$(APP_NAME)
	@chmod +x $(BUILD_DIR)/Sussurro.app/Contents/MacOS/$(APP_NAME)
	@cp release/icons/Sussurro.icns $(BUILD_DIR)/Sussurro.app/Contents/Resources/Sussurro.icns
	@sed "s/__VERSION__/$(VERSION)/g" release-templates/Info.plist \
		> $(BUILD_DIR)/Sussurro.app/Contents/Info.plist
	@printf "APPL????" > $(BUILD_DIR)/Sussurro.app/Contents/PkgInfo
	@touch $(BUILD_DIR)/Sussurro.app
	@echo "  ✓ $(BUILD_DIR)/Sussurro.app"
else
	@echo "make app: Sussurro.app is macOS-only. Use 'make package' on Linux."
endif

# Produce a release tarball (sussurro-<os>-<arch>.tar.gz) in release/.
# On macOS the tarball includes Sussurro.app; on Linux it includes the
# .desktop entry and hicolor icon set.
package: build
	@chmod +x scripts/package-release.sh
	@./scripts/package-release.sh

clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@rm -rf third_party
	@rm -rf .build-compat
	@rm -rf release/icons release/sussurro-*
