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

# Steps 8 to 10 below are idempotent (each is guarded by its own grep), so
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

# 10. SetInitialPrompt allocates a C string and never releases the previous
# one, so every streaming pass that sets the prompt leaks it. whisper_full
# tokenises the prompt into its own buffer during the call, and the Params
# value in the Go context is the only owner of the pointer, so freeing the
# old string before replacing it is safe. Guarded by a marker so it applies
# once, and the exact-line checks refuse a bindings file that has moved on.
PARAMS_GO="$WHISPER_DIR/bindings/go/params.go"
PROMPT_FREE_MARKER="Sussurro: release the previous initial prompt"
if [ -f "$PARAMS_GO" ] && ! grep -q "$PROMPT_FREE_MARKER" "$PARAMS_GO"; then
        tmp_file="$(mktemp)"
        awk -v marker="$PROMPT_FREE_MARKER" '
        /^import \($/ && !imports_done {
            print $0
            getline fmt_line
            getline close_line
            if (fmt_line != "\t\"fmt\"" || close_line != ")") exit 2
            print fmt_line
            print "\t\"unsafe\""
            print close_line
            imports_done = 1
            next
        }
        /^#include <whisper.h>$/ && !include_done {
            print "#include <stdlib.h>"
            print $0
            include_done = 1
            next
        }
        /^func \(p \*Params\) SetInitialPrompt\(prompt string\) \{$/ {
            print $0
            getline old_assign
            if (old_assign != "\tp.initial_prompt = C.CString(prompt)") exit 2
            print "\t// " marker " so repeated calls do not leak it."
            print "\tif p.initial_prompt != nil {"
            print "\t\tC.free(unsafe.Pointer(p.initial_prompt))"
            print "\t}"
            print old_assign
            patched_prompt = 1
            next
        }
        { print }
        END {
            if (!imports_done || !include_done || !patched_prompt) exit 1
        }
    ' "$PARAMS_GO" >"$tmp_file" || {
                rm -f "$tmp_file"
                echo "ERROR: initial prompt free patch did not match bindings/go/params.go" >&2
                exit 1
        }
        mv "$tmp_file" "$PARAMS_GO"
fi

# 11. Expose whisper.cpp's abort_callback through the Go binding. ggml polls it
# between graph nodes, so a transcription in flight can be cancelled within
# milliseconds. The streamer needs this: on a CPU-only host a partial pass on
# the large model runs 6-20s while holding the engine, and without abort the
# final transcription queued behind it waited the whole time (measured: 34s
# from key release to text with the v2.5 Linux release). The binding only
# offered encoder_begin_callback, which runs once before the encoder and so
# cannot interrupt the encoder itself. Guarded by a marker so it applies once,
# and the exact-line checks refuse a bindings file that has moved on.
ABORT_MARKER="Sussurro: abort callback"
if [ -f "$BINDINGS_GO" ] && ! grep -q "$ABORT_MARKER" "$BINDINGS_GO"; then
        tmp_file="$(mktemp)"
        awk -v marker="$ABORT_MARKER" '
        /^extern bool callEncoderBegin\(void\* user_data\);$/ && !extern_done {
            print $0
            print "extern bool callAbort(void* user_data);"
            extern_done = 1
            next
        }
        /^\/\/ Get default parameters and set callbacks$/ && !cb_done {
            print "// " marker ": polled by ggml between graph nodes; true aborts the run."
            print "static bool whisper_abort_cb(void* user_data) {"
            print "    if(user_data != NULL) {"
            print "        return callAbort(user_data);"
            print "    }"
            print "    return false;"
            print "}"
            print ""
            print $0
            cb_done = 1
            next
        }
        /^\tparams\.encoder_begin_callback_user_data = \(void\*\)\(ctx\);$/ && !params_done {
            print $0
            print "\tparams.abort_callback = whisper_abort_cb;"
            print "\tparams.abort_callback_user_data = (void*)(ctx);"
            params_done = 1
            next
        }
        /^\tcbEncoderBegin = make\(map\[unsafe\.Pointer\]func\(\) bool\)$/ && !map_done {
            print $0
            print "\tcbAbort        = make(map[unsafe.Pointer]func() bool)"
            map_done = 1
            next
        }
        /^\/\/export callEncoderBegin$/ && !go_done {
            print "// Whisper_set_abort_callback installs fn as the abort callback for every"
            print "// following Whisper_full on this context; nil removes it. Unlike the other"
            print "// callbacks it is not scoped to a single call: the caller owns its lifetime."
            print "// Call it only while no Whisper_full is running on ctx."
            print "func (ctx *Context) Whisper_set_abort_callback(fn func() bool) {"
            print "\tif fn == nil {"
            print "\t\tdelete(cbAbort, unsafe.Pointer(ctx))"
            print "\t} else {"
            print "\t\tcbAbort[unsafe.Pointer(ctx)] = fn"
            print "\t}"
            print "}"
            print ""
            print "//export callAbort"
            print "func callAbort(user_data unsafe.Pointer) C.bool {"
            print "\tif fn, ok := cbAbort[user_data]; ok && fn() {"
            print "\t\treturn C.bool(true)"
            print "\t}"
            print "\treturn C.bool(false)"
            print "}"
            print ""
            print $0
            go_done = 1
            next
        }
        { print }
        END {
            if (!extern_done || !cb_done || !params_done || !map_done || !go_done) exit 1
        }
    ' "$BINDINGS_GO" >"$tmp_file" || {
                rm -f "$tmp_file"
                echo "ERROR: abort callback patch did not match bindings/go/whisper.go" >&2
                exit 1
        }
        mv "$tmp_file" "$BINDINGS_GO"
fi

# The high-level pkg/whisper Context hides the low-level handle, so surface the
# abort callback there too.
CONTEXT_GO="$WHISPER_DIR/bindings/go/pkg/whisper/context.go"
INTERFACE_GO="$WHISPER_DIR/bindings/go/pkg/whisper/interface.go"
if [ -f "$CONTEXT_GO" ] && ! grep -q "$ABORT_MARKER" "$CONTEXT_GO"; then
        tmp_file="$(mktemp)"
        awk -v marker="$ABORT_MARKER" '
        /^\/\/ Process new sample data and return any errors$/ && !done {
            print "// SetAbortCallback installs fn, polled by ggml between graph nodes during"
            print "// Process; returning true aborts the run and Process returns an error."
            print "// nil removes it. " marker "."
            print "func (context *context) SetAbortCallback(fn func() bool) {"
            print "\tcontext.model.ctx.Whisper_set_abort_callback(fn)"
            print "}"
            print ""
            print $0
            done = 1
            next
        }
        { print }
        END { if (!done) exit 1 }
    ' "$CONTEXT_GO" >"$tmp_file" || {
                rm -f "$tmp_file"
                echo "ERROR: abort callback patch did not match bindings/go/pkg/whisper/context.go" >&2
                exit 1
        }
        mv "$tmp_file" "$CONTEXT_GO"
fi
if [ -f "$INTERFACE_GO" ] && ! grep -q "$ABORT_MARKER" "$INTERFACE_GO"; then
        tmp_file="$(mktemp)"
        awk -v marker="$ABORT_MARKER" '
        /^\tProcess\(\[\]float32, EncoderBeginCallback, SegmentCallback, ProgressCallback\) error$/ && !done {
            print $0
            print "\t// " marker ": polled during Process; true aborts the run."
            print "\tSetAbortCallback(func() bool)"
            done = 1
            next
        }
        { print }
        END { if (!done) exit 1 }
    ' "$INTERFACE_GO" >"$tmp_file" || {
                rm -f "$tmp_file"
                echo "ERROR: abort callback patch did not match bindings/go/pkg/whisper/interface.go" >&2
                exit 1
        }
        mv "$tmp_file" "$INTERFACE_GO"
fi

# 12. Make abort_callback interrupt a running encoder/decoder graph. Upstream
# only consults it *between* graphs (after the whole encoder has finished),
# because the scheduler-based compute helper never installs it on the CPU
# backend, where ggml would poll it per node. With the large model on CPU the
# encoder alone runs 6-20s, so an "aborted" partial pass still held the engine
# for its full duration (measured: 21.8s) and the final transcription waited.
# The helper gains two defaulted parameters and the encoder/decoder call sites
# pass the params' callback through. GPU backends expose no abort hook, and
# need none: their passes take ~100ms. Guarded by a marker so it applies once.
WHISPER_CPP="$WHISPER_DIR/src/whisper.cpp"
SCHED_ABORT_MARKER="Sussurro: install abort_callback on the backends"
if [ -f "$WHISPER_CPP" ] && ! grep -q "$SCHED_ABORT_MARKER" "$WHISPER_CPP"; then
        tmp_file="$(mktemp)"
        awk -v marker="$SCHED_ABORT_MARKER" '
        # Signature: `bool sched_reset = true) {` is the last parameter line of
        # the sched-based helper only (the plain helper takes abort params).
        /^                      bool   sched_reset = true\) \{$/ && !sig_done {
            print "                      bool   sched_reset = true,"
            print "         wsp_ggml_abort_callback   abort_callback = nullptr,"
            print "                        void * abort_callback_data = nullptr) {"
            sig_done = 1
            next
        }
        /^        if \(fn_set_n_threads\) \{$/ && sig_done && !body_done {
            print "        // " marker " so ggml polls it per node."
            print "        auto * fn_set_abort = (wsp_ggml_backend_set_abort_callback_t) wsp_ggml_backend_reg_get_proc_address(reg, \"wsp_ggml_backend_set_abort_callback\");"
            print "        if (fn_set_abort) {"
            print "            fn_set_abort(backend, abort_callback, abort_callback_data);"
            print "        }"
            print $0
            body_done = 1
            next
        }
        /if \(!wsp_ggml_graph_compute_helper\(sched, gf, n_threads\)\) \{$/ {
            sub(/wsp_ggml_graph_compute_helper\(sched, gf, n_threads\)/, "wsp_ggml_graph_compute_helper(sched, gf, n_threads, true, abort_callback, abort_callback_data)")
            calls++
        }
        { print }
        END {
            if (!sig_done || !body_done || calls != 4) exit 1
        }
    ' "$WHISPER_CPP" >"$tmp_file" || {
                rm -f "$tmp_file"
                echo "ERROR: sched abort patch did not match src/whisper.cpp" >&2
                exit 1
        }
        mv "$tmp_file" "$WHISPER_CPP"
fi

echo "Patch applied successfully."
