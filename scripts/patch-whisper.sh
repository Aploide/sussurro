#!/bin/bash
# scripts/patch-whisper.sh
# Patch whisper.cpp to rename ggml and gguf symbols to avoid conflict with go-llama.cpp

set -e

WHISPER_DIR="third_party/whisper.cpp"

if [ ! -d "$WHISPER_DIR" ]; then
    echo "Directory $WHISPER_DIR does not exist. Run 'make deps' first."
    exit 1
fi

echo "Patching whisper.cpp to rename ggml and gguf symbols..."

# Detect OS for sed syntax (macOS requires -i '', Linux requires -i)
if [[ "$OSTYPE" == "darwin"* ]]; then
    SED_INPLACE="sed -i ''"
else
    SED_INPLACE="sed -i"
fi

# 1. Rename symbols in C/C++/Go/CMake files
# We replace:
# ggml_ -> wsp_ggml_
# GGML_ -> WSP_GGML_
# gguf_ -> wsp_gguf_
# GGUF_ -> WSP_GGUF_
# quantize_row_ -> wsp_quantize_row_ (and related functions)
find "$WHISPER_DIR" -type f \( -name "*.c" -o -name "*.cpp" -o -name "*.h" -o -name "*.cu" -o -name "*.m" -o -name "*.go" -o -name "*.metal" -o -name "CMakeLists.txt" -o -name "*.cmake" \) -not -path "*/.git/*" -print0 | xargs -0 $SED_INPLACE \
    -e 's/ggml_/wsp_ggml_/g' \
    -e 's/GGML_/WSP_GGML_/g' \
    -e 's/gguf_/wsp_gguf_/g' \
    -e 's/GGUF_/WSP_GGUF_/g' \
    -e 's/ggml::/wsp_ggml::/g' \
    -e 's/namespace ggml/namespace wsp_ggml/g' \
    -e 's/quantize_row_/wsp_quantize_row_/g' \
    -e 's/dequantize_row_/wsp_dequantize_row_/g' \
    -e 's/quantize_iq/wsp_quantize_iq/g' \
    -e 's/quantize_q/wsp_quantize_q/g' \
    -e 's/quantize_tq/wsp_quantize_tq/g' \
    -e 's/quantize_mxfp/wsp_quantize_mxfp/g' \
    -e 's/quantize_nvfp4/wsp_quantize_nvfp4/g' \
    -e 's/iq2xs_/wsp_iq2xs_/g' \
    -e 's/iq3xs_/wsp_iq3xs_/g'

# 2. Revert changes to #include directives
# Since we didn't rename the actual files (e.g. ggml.h is still ggml.h),
# we must revert #include "wsp_ggml.h" back to #include "ggml.h"
find "$WHISPER_DIR" -type f \( -name "*.c" -o -name "*.cpp" -o -name "*.h" -o -name "*.cu" -o -name "*.m" -o -name "*.go" \) -not -path "*/.git/*" -print0 | xargs -0 $SED_INPLACE \
    -e 's/#include "wsp_ggml/#include "ggml/g' \
    -e 's/#include <wsp_ggml/#include <ggml/g' \
    -e 's/#include "wsp_gguf/#include "gguf/g' \
    -e 's/#include <wsp_gguf/#include <gguf/g'

# 3. Fix specific include path for ggml-metal-device.h which fails to find ggml.h
if [ -f "$WHISPER_DIR/ggml/src/ggml-metal/ggml-metal-device.h" ]; then
    $SED_INPLACE 's/#include "ggml.h"/#include "..\/..\/include\/ggml.h"/g' "$WHISPER_DIR/ggml/src/ggml-metal/ggml-metal-device.h"
fi

# 4. Fix specific include path for ggml-impl.h which fails to find ggml.h and gguf.h
if [ -f "$WHISPER_DIR/ggml/src/ggml-impl.h" ]; then
    $SED_INPLACE 's/#include "ggml.h"/#include "..\/include\/ggml.h"/g' "$WHISPER_DIR/ggml/src/ggml-impl.h"
    $SED_INPLACE 's/#include "gguf.h"/#include "..\/include\/gguf.h"/g' "$WHISPER_DIR/ggml/src/ggml-impl.h"
fi

# 5. Fix specific include path for ggml-backend-impl.h which fails to find ggml-backend.h
if [ -f "$WHISPER_DIR/ggml/src/ggml-backend-impl.h" ]; then
    $SED_INPLACE 's/#include "ggml-backend.h"/#include "..\/include\/ggml-backend.h"/g' "$WHISPER_DIR/ggml/src/ggml-backend-impl.h"
fi

# 6. Fix Mach-O section name length error in ggml-metal/CMakeLists.txt
if [ -f "$WHISPER_DIR/ggml/src/ggml-metal/CMakeLists.txt" ]; then
    $SED_INPLACE 's/__wsp_ggml_metallib/__wsp_ggml_mtl/g' "$WHISPER_DIR/ggml/src/ggml-metal/CMakeLists.txt"
fi

# 7. Ensure Go bindings can locate headers and static libs without external env vars
BINDINGS_GO="$WHISPER_DIR/bindings/go/whisper.go"
if [ -f "$BINDINGS_GO" ]; then
    if ! grep -q '#cgo CFLAGS: -I${SRCDIR}/../../include -I${SRCDIR}/../../ggml/include' "$BINDINGS_GO"; then
        tmp_file="$(mktemp)"
        awk '
            {
                if (!inserted && $0 == "#cgo LDFLAGS: -lwhisper -lggml -lggml-base -lggml-cpu -lm -lstdc++") {
                    print "#cgo CFLAGS: -I${SRCDIR}/../../include -I${SRCDIR}/../../ggml/include"
                    print "#cgo LDFLAGS: -L${SRCDIR}/../../build/src -L${SRCDIR}/../../build/ggml/src -L${SRCDIR}/../../build/ggml/src/ggml-cpu -L${SRCDIR}/../../build/ggml/src/ggml-blas"
                    print "#cgo darwin LDFLAGS: -L${SRCDIR}/../../build/ggml/src/ggml-metal"
                    inserted = 1
                }
                print
            }
        ' "$BINDINGS_GO" > "$tmp_file"
        mv "$tmp_file" "$BINDINGS_GO"
    fi
fi

echo "Patch applied successfully."
