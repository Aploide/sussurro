#!/bin/bash
# scripts/patch-llama-windows.sh
# Patch go-llama.cpp for Windows (MinGW) builds.

set -e

LLAMA_DIR="third_party/go-llama.cpp"

if [ ! -d "$LLAMA_DIR" ]; then
    echo "Directory $LLAMA_DIR does not exist. Run 'make deps' first."
    exit 1
fi

# CMake on Windows emits *.obj object files, but the libbinding.a recipe only
# collects *.o, which would leave the archive nearly empty. Widen the find
# filter and the final ar glob (the copied files keep their .obj extension).
if ! grep -q 'name "\*\.obj"' "$LLAMA_DIR/Makefile"; then
    sed -i 's/-name "\*\.o" -type f/\\( -name "*.o" -o -name "*.obj" \\) -type f/' "$LLAMA_DIR/Makefile"
    if ! grep -q 'name "\*\.obj"' "$LLAMA_DIR/Makefile"; then
        echo "ERROR: find .obj patch did not apply — go-llama.cpp Makefile layout changed." >&2
        exit 1
    fi
    echo "Patched go-llama.cpp Makefile (find .obj fix)."
fi

if ! grep -q 'ar rcs libbinding.a obj_temp/\*$' "$LLAMA_DIR/Makefile"; then
    sed -i 's|ar rcs libbinding.a obj_temp/\*\.o$|ar rcs libbinding.a obj_temp/*|' "$LLAMA_DIR/Makefile"
    if ! grep -q 'ar rcs libbinding.a obj_temp/\*$' "$LLAMA_DIR/Makefile"; then
        echo "ERROR: ar glob patch did not apply — go-llama.cpp Makefile layout changed." >&2
        exit 1
    fi
    echo "Patched go-llama.cpp Makefile (ar glob fix)."
fi
