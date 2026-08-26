APP_NAME := sussurro
BUILD_DIR := bin
CMD_DIR := cmd/sussurro

# Version stamped into the binaries. Release builds pass VERSION=<git tag>
# (see .github/workflows/release.yml); local builds leave it empty and the
# binary reports the "dev" default from internal/version.
VERSION ?=
VERSION_PKG := github.com/aploide/sussurro/internal/version
GO_LDFLAGS = $(VERSION:%=-ldflags "-X $(VERSION_PKG).Version=%")

# Whisper.cpp configuration
WHISPER_DIR := third_party/whisper.cpp
WHISPER_INCLUDE := $(abspath $(WHISPER_DIR)/include)
WHISPER_GGML_INCLUDE := $(abspath $(WHISPER_DIR)/ggml/include)
C_INCLUDE_PATH := $(WHISPER_INCLUDE):$(WHISPER_GGML_INCLUDE)
LIBRARY_PATH := $(abspath $(WHISPER_DIR))

# go-llama.cpp configuration
LLAMA_DIR := third_party/go-llama.cpp

# Pinned dependency revisions for reproducible builds.
# WHISPER_COMMIT matches the whisper.cpp bindings pseudo-version in go.mod;
# GO_LLAMA_COMMIT is the fork's main HEAD (llama.cpp submodule recent enough for Qwen3).
WHISPER_COMMIT ?= 764482c3175d9c3bc6089c1ec84df7d1b9537d83
GO_LLAMA_COMMIT ?= b2c101738f26f466f1a30317d50a88ce7c0ada12
WHISPER_BACKEND := cpu
LLAMA_BACKEND := cpu
WHISPER_CMAKE_EXTRA := -DWSP_GGML_VULKAN=OFF
LLAMA_CMAKE_ARGS := -DGGML_VULKAN=OFF
EXE :=
WIN_LDFLAGS :=
GGML_VULKAN_PATH :=
VULKAN_LDFLAGS :=

# Stamp files marking a completed native build, so `deps` is a no-op once the
# libraries exist. Each stamp includes the commit and native backend, so a pin
# or backend change invalidates it and forces the required reconfiguration.
#
# Without these, every `make build` and `make test` reconfigured whisper.cpp and
# ran `make clean` on go-llama.cpp before recompiling it from scratch, which
# cost about four and a half minutes per invocation even when nothing had
# changed at all.
WHISPER_STAMP = $(WHISPER_DIR)/.stamp-$(WHISPER_COMMIT)-$(WHISPER_BACKEND)
LLAMA_STAMP   = $(LLAMA_DIR)/.stamp-$(GO_LLAMA_COMMIT)-$(LLAMA_BACKEND)

# Detect number of CPU cores for parallel builds
# Build parallelism. Defaults to 50% of the cores so a rebuild leaves the
# machine usable — these builds are long, and saturating every core makes the
# desktop unresponsive for their duration. This is a default, not a cap:
# override with e.g. BUILD_JOBS=24 to use everything.
NCORES    := $(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 1)
BUILD_JOBS ?= $(shell awk 'BEGIN { n = int($(NCORES) / 2); print n < 1 ? 1 : n }')
NPROCS    := $(BUILD_JOBS)

# Run compilers at low CPU and IO priority, so an interactive desktop keeps
# its responsiveness even while a long build saturates its share of cores.
# Both tools are optional; the build works without them.
NICE := $(shell command -v nice >/dev/null 2>&1 && echo "nice -n 19")
NICE += $(shell command -v ionice >/dev/null 2>&1 && echo "ionice -c 3")

# Detect OS and architecture for platform-specific builds
UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)
# Generic ShellCheck integrations otherwise parse GNU Make conditionals as
# shell function declarations and stop before checking any recipe bodies.
# shellcheck disable=SC1073,SC1065,SC1064,SC1072
ifeq ($(UNAME_S),Darwin)
	BUILD_TYPE := metal
	WHISPER_BACKEND := metal
	WHISPER_CMAKE_EXTRA := -DWSP_GGML_METAL=ON -DWSP_GGML_VULKAN=OFF
	LLAMA_CMAKE_ARGS := -DLLAMA_METAL=ON -DGGML_VULKAN=OFF
	LLAMA_BACKEND := metal
	GGML_METAL_PATH := -L$(WHISPER_DIR)/build/ggml/src/ggml-metal
else
	BUILD_TYPE :=
	GGML_METAL_PATH :=
endif

# Windows (MSYS2 MINGW64): Vulkan-accelerated whisper.cpp, CPU go-llama.cpp.
# go-llama.cpp has no Vulkan BUILD_TYPE in its upstream cross-build Makefile.
ifeq ($(OS),Windows_NT)
	EXE := .exe
	WHISPER_BACKEND := vulkan
	# patch-whisper.sh renames the GGML_ CMake options too, hence WSP_GGML_VULKAN.
	WHISPER_CMAKE_EXTRA := -G Ninja -DWSP_GGML_VULKAN=ON
	GGML_VULKAN_PATH := -L$(WHISPER_DIR)/build/ggml/src/ggml-vulkan
	# go-llama.cpp: skip llama.cpp's cli/server tools, and give the vendored
	# cpp-httplib a Windows 10 baseline (MinGW's default _WIN32_WINNT is older).
	LLAMA_CMAKE_ARGS := -DGGML_VULKAN=OFF -DLLAMA_BUILD_TOOLS=OFF -DLLAMA_BUILD_APP=OFF -DLLAMA_BUILD_SERVER=OFF \
		-DCMAKE_CXX_FLAGS=-D_WIN32_WINNT=0x0A00
	# --start-group makes ld re-scan the static archives regardless of ordering;
	# trailing libs cover the Vulkan loader, libstdc++ for ggml-vulkan, and the
	# OpenMP runtime go-llama.cpp builds against (its -fopenmp LDFLAG is
	# linux-tagged, so it must be supplied here). -static keeps every -l
	# (including the bindings' own -lstdc++) on the static archives — mixing
	# libstdc++.a with libstdc++.dll.a causes duplicate-symbol errors — and
	# yields an exe with no MinGW runtime DLL dependencies.
	# vulkan-1 must stay dynamic: there is no static Vulkan loader (the DLL is
	# shipped by Windows / the GPU driver), so toggle -Bdynamic just for it.
	WIN_LDFLAGS := -Wl,--start-group -lwhisper -lggml -lggml-base -lggml-cpu -lggml-vulkan -Wl,--end-group \
		-Wl,-Bdynamic -lvulkan-1 -Wl,-Bstatic -lstdc++ -fopenmp -static
	# MinGW gcc expects ';'-separated C_INCLUDE_PATH/LIBRARY_PATH; the patched
	# bindings carry their own -I/-L flags, so the env vars are not needed.
	C_INCLUDE_PATH :=
	LIBRARY_PATH :=
else ifeq ($(UNAME_S),Linux)
	# Linux: offload the bundled LLM through Vulkan when the SDK is present.
	# Whisper remains on CPU because the two dependencies vendor different GGML
	# revisions; loading both Vulkan runtimes in one process is not ABI-safe.
	# Vulkan covers AMD, Intel, and NVIDIA without a vendor runtime install.
	#
	# Opt out with LLAMA_VULKAN=0 for a pure CPU build.
	LLAMA_VULKAN ?= auto
	ifeq ($(LLAMA_VULKAN),auto)
		HAS_VULKAN := $(shell pkg-config --exists vulkan 2>/dev/null && command -v glslc >/dev/null 2>&1 && echo yes || echo no)
	else ifeq ($(LLAMA_VULKAN),0)
		HAS_VULKAN := no
	else
		HAS_VULKAN := yes
	endif
	ifeq ($(HAS_VULKAN),yes)
		LLAMA_CMAKE_ARGS := -DGGML_VULKAN=ON
		LLAMA_BACKEND := vulkan
		VULKAN_LDFLAGS := -lvulkan
	endif
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
ifneq ($(OS),Windows_NT)
# The pkg-config module is gtk-layer-shell-0; "gtk-layer-shell" is the *package*
# name on most distros and never resolves, so probe both (older 0.6 releases
# shipped only the unsuffixed .pc).
LAYER_SHELL_PC     := $(shell pkg-config --exists gtk-layer-shell-0 2>/dev/null && echo gtk-layer-shell-0 || (pkg-config --exists gtk-layer-shell 2>/dev/null && echo gtk-layer-shell))
HAS_LAYER_SHELL    := $(if $(LAYER_SHELL_PC),yes,no)

LAYER_CFLAGS  := $(shell pkg-config --cflags gtk+-3.0 2>/dev/null)
LAYER_LDFLAGS := $(shell pkg-config --libs   gtk+-3.0 2>/dev/null)

ifeq ($(HAS_LAYER_SHELL),yes)
LAYER_CFLAGS  += $(shell pkg-config --cflags $(LAYER_SHELL_PC) 2>/dev/null) -DHAVE_GTK_LAYER_SHELL
LAYER_LDFLAGS += $(shell pkg-config --libs   $(LAYER_SHELL_PC) 2>/dev/null)
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

# The tray uses fyne.io/systray, whose Linux backend is pure Go over the DBus
# StatusNotifierItem protocol. No libappindicator / libayatana-appindicator is
# linked, so there is no backend to select at build time.
UI_TAGS :=
endif  # !Windows_NT

# Base CGO link flags (whisper + llama)
VULKAN_LDFLAGS ?=
BASE_LDFLAGS := -L$(WHISPER_DIR)/build/src -L$(WHISPER_DIR)/build/ggml/src \
	-L$(WHISPER_DIR)/build/ggml/src/ggml-cpu $(GGML_METAL_PATH) \
	-L$(WHISPER_DIR)/build/ggml/src/ggml-blas -lwhisper

# Every Go binary that imports Whisper must link the backend compiled into its
# static ggml archive. Keep this platform-aware set shared so auxiliary tools
# cannot silently drift from the main application.
ifeq ($(OS),Windows_NT)
WHISPER_LDFLAGS := $(BASE_LDFLAGS) $(GGML_VULKAN_PATH) $(WIN_LDFLAGS)
else
WHISPER_LDFLAGS := $(BASE_LDFLAGS) $(GGML_VULKAN_PATH) $(VULKAN_LDFLAGS)
endif

# Export environment variables for CGO
export C_INCLUDE_PATH
export LIBRARY_PATH

# The stamp targets are deliberately absent: they are real files, and marking
# them phony would defeat the guard entirely.
.PHONY: all build compat-pc run clean clean-deps check-deps-artefacts deps test test-settings-geometry

# Packages that link whisper need the same CGO_LDFLAGS as the binary: with
# Vulkan enabled, a plain "go test ./..." cannot resolve the backend symbols.
# Use this target rather than calling go test directly.
#
# GOTESTFLAGS and PKGS narrow a run without a full-suite round trip, e.g.
#   make test PKGS=./internal/pipeline/ GOTESTFLAGS="-count=1 -run TestWindow"
# Unrecognised variables are silently ignored by make, so a typo here reads as
# a passing full run; check the echoed command line when narrowing.
PKGS ?= ./internal/... ./cmd/...

test: deps compat-pc
	PKG_CONFIG_PATH="$(PKG_CONFIG_PATH_UI)" \
	CGO_CFLAGS="$(LAYER_CFLAGS) $(WV_CFLAGS)" \
	CGO_LDFLAGS="$(WHISPER_LDFLAGS) $(LAYER_LDFLAGS) $(WV_LDFLAGS)" \
	$(NICE) go test $(UI_TAGS) $(if $(RACE),-race) $(GOTESTFLAGS) $(PKGS)

test-settings-geometry: deps compat-pc
ifeq ($(UNAME_S),Linux)
	@test -n "$$DISPLAY" || { echo "test-settings-geometry requires an X display (for CI, run under Xvfb)"; exit 1; }
	PKG_CONFIG_PATH="$(PKG_CONFIG_PATH_UI)" \
	CGO_CFLAGS="$(LAYER_CFLAGS) $(WV_CFLAGS)" \
	CGO_LDFLAGS="$(WHISPER_LDFLAGS) $(LAYER_LDFLAGS) $(WV_LDFLAGS)" \
	$(NICE) go test -tags settings_geometry -count=1 -timeout 25s -run '^TestRenderedSettingsGeometryReportsEveryTab$$' ./internal/ui
else
	@echo "test-settings-geometry is currently supported only on Linux/WebKit"; exit 1
endif

all: build build-transcribe

# deps is satisfied by the two stamps; when both exist and their pins are
# unchanged, it does nothing at all.
#
# A stamp on its own would still be trusted if the library it vouches for had
# been deleted, and the failure would surface as a confusing link error rather
# than a rebuild. Checking for the artefacts here drops the stale stamps first,
# so the rules below fire again.
deps: check-deps-artefacts $(WHISPER_STAMP) $(LLAMA_STAMP)

# Removes a stamp whose library has gone missing. Silent when all is well.
check-deps-artefacts:
	@if [ -f "$(WHISPER_STAMP)" ] && [ ! -f "$(WHISPER_DIR)/build/src/libwhisper.a" ]; then \
		echo "whisper.cpp library missing; rebuilding"; \
		rm -f $(WHISPER_STAMP); \
	fi
	@if [ -f "$(LLAMA_STAMP)" ] && [ ! -f "$(LLAMA_DIR)/libbinding.a" ]; then \
		echo "go-llama.cpp library missing; rebuilding"; \
		rm -f $(LLAMA_STAMP); \
	fi

$(WHISPER_STAMP): scripts/patch-whisper.sh
	@mkdir -p third_party
	@if [ ! -d "$(WHISPER_DIR)" ]; then \
		echo "Cloning whisper.cpp..."; \
		git clone https://github.com/ggerganov/whisper.cpp.git $(WHISPER_DIR); \
		git -C $(WHISPER_DIR) checkout --quiet $(WHISPER_COMMIT); \
	fi
	@echo "Patching whisper.cpp..."
	@chmod +x scripts/patch-whisper.sh
	@./scripts/patch-whisper.sh
	@echo "Building whisper.cpp library..."
	@cmake -S $(WHISPER_DIR) -B $(WHISPER_DIR)/build \
		-DGGML_NATIVE=OFF \
		-DBUILD_SHARED_LIBS=OFF \
		-DWHISPER_BUILD_TESTS=OFF \
		-DWHISPER_BUILD_EXAMPLES=OFF \
		$(WHISPER_CMAKE_EXTRA) \
		$(if $(ARM_COMPAT_CFLAGS),-DCMAKE_C_FLAGS="$(ARM_COMPAT_CFLAGS)" -DCMAKE_CXX_FLAGS="$(ARM_COMPAT_CFLAGS)")
	@$(NICE) cmake --build $(WHISPER_DIR)/build --config Release --target whisper -j $(NPROCS)
ifeq ($(OS),Windows_NT)
	@# The renamed CMake targets emit ggml archives without the "lib" prefix on
	@# Windows; provide lib-prefixed copies so -lggml/-lggml-vulkan/... resolve.
	@for d in "$(WHISPER_DIR)/build/ggml/src" "$(WHISPER_DIR)/build/ggml/src/ggml-vulkan" "$(WHISPER_DIR)/build/ggml/src/ggml-cpu"; do \
		for f in "$$d"/ggml*.a; do \
			[ -f "$$f" ] || continue; \
			base=$$(basename "$$f"); \
			case "$$base" in lib*) continue;; esac; \
			cp -f "$$f" "$$d/lib$$base"; \
		done; \
	done
endif
	@rm -f $(WHISPER_DIR)/.stamp-*
	@touch $@

$(LLAMA_STAMP):
	@mkdir -p third_party
	@if [ ! -d "$(LLAMA_DIR)" ]; then \
		echo "Cloning go-llama.cpp..."; \
		git clone --recursive https://github.com/AshkanYarmoradi/go-llama.cpp $(LLAMA_DIR); \
		git -C $(LLAMA_DIR) checkout --quiet $(GO_LLAMA_COMMIT); \
		git -C $(LLAMA_DIR) submodule update --init --recursive; \
	fi
	@# A backend or pin change must reconfigure CMake and rebuild the archive.
	@# The dependency Makefile does not declare binding.cpp as an object input,
	@# and otherwise trusts build/build_complete from the previous backend.
	@rm -f $(LLAMA_DIR)/build/build_complete $(LLAMA_DIR)/binding.o $(LLAMA_DIR)/libbinding.a
ifeq ($(OS),Windows_NT)
	@echo "Patching go-llama.cpp for Windows..."
	@chmod +x scripts/patch-llama-windows.sh
	@./scripts/patch-llama-windows.sh
endif
	@echo "Building go-llama.cpp library..."
	@# Do not run `make clean` here: it deletes the whole build tree and turns a
	@# backend switch into a four-minute rebuild. Removing the three targets
	@# above keeps the CMake object cache while forcing correct reconfiguration,
	@# binding compilation, and archive assembly.
ifeq ($(OS),Windows_NT)
	@$(NICE) $(MAKE) -j $(NPROCS) -C $(LLAMA_DIR) libbinding.a BUILD_TYPE=$(BUILD_TYPE) CMAKE_ARGS="$(LLAMA_CMAKE_ARGS)"
else
	@$(NICE) $(MAKE) -j $(NPROCS) -C $(LLAMA_DIR) libbinding.a BUILD_TYPE=$(BUILD_TYPE) CMAKE_ARGS="$(LLAMA_CMAKE_ARGS)"
endif
	@rm -f $(LLAMA_DIR)/.stamp-*
	@touch $@

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
ifeq ($(OS),Windows_NT)
	CGO_LDFLAGS="$(WHISPER_LDFLAGS)" \
	go build $(GO_LDFLAGS) -o $(BUILD_DIR)/$(APP_NAME)$(EXE) ./$(CMD_DIR)
else ifeq ($(UNAME_S),Darwin)
	CGO_LDFLAGS="$(WHISPER_LDFLAGS) -framework Cocoa -framework QuartzCore -framework CoreVideo -framework Foundation" \
	go build $(GO_LDFLAGS) -o $(BUILD_DIR)/$(APP_NAME) ./$(CMD_DIR)
else
	@echo "  Layer shell  : $(HAS_LAYER_SHELL)$(if $(LAYER_SHELL_PC), ($(LAYER_SHELL_PC)))"
	@echo "  Vulkan       : $(HAS_VULKAN)"
	@echo "  Build jobs   : $(BUILD_JOBS) of $(NCORES) cores ($(NICE))"
	@echo "  Build tags   : $(UI_TAGS)"
	PKG_CONFIG_PATH="$(PKG_CONFIG_PATH_UI)" \
	CGO_CFLAGS="$(LAYER_CFLAGS) $(WV_CFLAGS)" \
	CGO_LDFLAGS="$(WHISPER_LDFLAGS) $(LAYER_LDFLAGS) $(WV_LDFLAGS)" \
	$(NICE) go build $(UI_TAGS) $(GO_LDFLAGS) -o $(BUILD_DIR)/$(APP_NAME) ./$(CMD_DIR)
endif

# Build sussurro-transcribe CLI (no UI dependencies)
build-transcribe: deps
	@echo "Building sussurro-transcribe..."
	@mkdir -p $(BUILD_DIR)
ifeq ($(OS),Windows_NT)
	CGO_LDFLAGS="$(WHISPER_LDFLAGS)" \
	go build $(GO_LDFLAGS) -o $(BUILD_DIR)/sussurro-transcribe$(EXE) ./cmd/transcribe
else ifeq ($(UNAME_S),Darwin)
	CGO_LDFLAGS="$(WHISPER_LDFLAGS) -framework Accelerate -framework Foundation" \
	go build $(GO_LDFLAGS) -o $(BUILD_DIR)/sussurro-transcribe ./cmd/transcribe
else
	CGO_LDFLAGS="$(WHISPER_LDFLAGS)" \
	go build $(GO_LDFLAGS) -o $(BUILD_DIR)/sussurro-transcribe ./cmd/transcribe
endif

run: build
	@echo "Running $(APP_NAME)..."
	@./$(BUILD_DIR)/$(APP_NAME)

clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@rm -rf third_party
	@rm -rf .build-compat

# Force the native libraries to rebuild without re-cloning them. Dropping the
# stamps is enough: the next `make build` or `make test` rebuilds whatever is
# missing. Use this when a native build is suspected of being wrong, rather
# than `clean`, which discards the clones and their submodules too.
clean-deps:
	@echo "Dropping native build stamps..."
	@rm -f $(WHISPER_DIR)/.stamp-* $(LLAMA_DIR)/.stamp-*
	@if [ -d "$(LLAMA_DIR)" ]; then $(MAKE) -C $(LLAMA_DIR) clean; fi
