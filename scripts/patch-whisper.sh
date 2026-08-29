#!/bin/bash
# scripts/patch-whisper.sh
# Patch whisper.cpp to rename ggml and gguf symbols to avoid conflict with go-llama.cpp

set -e

WHISPER_DIR="${WHISPER_DIR:-third_party/whisper.cpp}"

if [ ! -d "$WHISPER_DIR" ]; then
        echo "Directory $WHISPER_DIR does not exist. Run 'make deps' first."
        exit 1
fi

# The rename is not idempotent: running it twice turns wsp_ggml_ into
# wsp_wsp_ggml_ and leaves the tree unbuildable, with CMake options renamed
# out from under the flags the Makefile passes. Detect an already-patched
# tree and stop before doing any damage.
if grep -rqs 'wsp_wsp_ggml_\|WSP_WSP_GGML_' "$WHISPER_DIR/ggml/include" 2>/dev/null; then
        echo "ERROR: $WHISPER_DIR is doubly patched (wsp_wsp_ggml_ present)." >&2
        echo "Restore it with: git -C $WHISPER_DIR checkout -- . && ./scripts/patch-whisper.sh" >&2
        exit 1
fi

if grep -rqs 'wsp_ggml_' "$WHISPER_DIR/ggml/include" 2>/dev/null; then
        echo "whisper.cpp is already patched; skipping symbol rename."
        ALREADY_PATCHED=1
else
        ALREADY_PATCHED=0
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
if [ "$ALREADY_PATCHED" = "0" ]; then
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

        # 7. Revert the quantize_q rename inside the Vulkan backend.
        # There, quantize_q8_1 is a SPIR-V shader: vulkan-shaders-gen resolves it to
        # the source file quantize_q8_1.comp (shader files are not renamed), so the
        # renamed name strings would make shader generation silently skip it and the
        # host references would never link. It is not a ggml quantization function,
        # so reverting cannot collide with go-llama.cpp's ggml.
        if [ -d "$WHISPER_DIR/ggml/src/ggml-vulkan" ]; then
                $SED_INPLACE 's/wsp_quantize_q8_1/quantize_q8_1/g' \
                        "$WHISPER_DIR/ggml/src/ggml-vulkan/ggml-vulkan.cpp" \
                        "$WHISPER_DIR/ggml/src/ggml-vulkan/vulkan-shaders/vulkan-shaders-gen.cpp"
        fi

fi # ALREADY_PATCHED

# Steps 8 and 9 below are idempotent (each is guarded by its own grep), so
# they run even on an already-patched tree — that is how a Vulkan rebuild
# picks up the new link flags without redoing the rename.

# 8. Ensure Go bindings can locate headers and static libs without external env vars
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
        ' "$BINDINGS_GO" >"$tmp_file"
                mv "$tmp_file" "$BINDINGS_GO"
        fi
fi

# Note: Vulkan link flags are deliberately NOT injected here. Enabling the
# backend needs -Wl,--whole-archive (ggml registers it from a static
# initialiser nothing references), and cgo rejects that flag inside a #cgo
# directive. The Makefile supplies it through CGO_LDFLAGS instead, which is
# not subject to that allowlist. See WHISPER_VULKAN in the Makefile.

# 9. whisper.cpp maps segment timestamps back to the original audio after VAD
# removes silence, but returns token timestamps on the compressed timeline.
# Sussurro uses token times for streaming window boundaries, so map token data
# through the same table. Keep this patch here because third_party is cloned at
# build time and is not part of the repository.
WHISPER_CPP="$WHISPER_DIR/src/whisper.cpp"
TOKEN_MAPPING_MARKER="Sussurro: map VAD-compressed token timestamps"
if [ -f "$WHISPER_CPP" ] && ! grep -q "$TOKEN_MAPPING_MARKER" "$WHISPER_CPP"; then
        tmp_file="$(mktemp)"
        awk '
        /^struct whisper_token_data whisper_full_get_token_data_from_state\(/ {
            print $0
            getline old_return
            getline old_close
            if (old_return != "    return state->result_all[i_segment].tokens[i_token];" || old_close != "}") exit 2
            print "    // Sussurro: map VAD-compressed token timestamps to original audio."
            print "    auto data = state->result_all[i_segment].tokens[i_token];"
            print "    if (state->has_vad_segments && !state->vad_mapping_table.empty()) {"
            print "        if (data.t0 >= 0) data.t0 = map_processed_to_original_time(data.t0, state->vad_mapping_table);"
            print "        if (data.t1 >= 0) data.t1 = map_processed_to_original_time(data.t1, state->vad_mapping_table);"
            print "    }"
            print "    return data;"
            print "}"
            patched_state = 1
            next
        }
        /^struct whisper_token_data whisper_full_get_token_data\(/ {
            print $0
            getline old_return
            getline old_close
            if (old_return != "    return ctx->state->result_all[i_segment].tokens[i_token];" || old_close != "}") exit 2
            print "    return whisper_full_get_token_data_from_state(ctx->state, i_segment, i_token);"
            print "}"
            patched_context = 1
            next
        }
        { print }
        END {
            if (!patched_state || !patched_context) exit 1
        }
    ' "$WHISPER_CPP" >"$tmp_file" || {
                rm -f "$tmp_file"
                echo "ERROR: VAD token timestamp patch did not match whisper.cpp" >&2
                exit 1
        }
        mv "$tmp_file" "$WHISPER_CPP"
fi

echo "Patch applied successfully."
