package asr

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/whisper.cpp/ggml/include
#include <stddef.h>
#include "ggml-backend.h"

// The device whisper.cpp will run on. Mirrors whisper_backend_init_gpu: with
// its default context params (use_gpu = true, gpu_device = 0) it takes the
// first registered GPU or integrated-GPU device and otherwise stays on the
// CPU. Returns NULL for the CPU case.
static const char *sussurro_asr_gpu_description(void) {
    size_t n = wsp_ggml_backend_dev_count();
    for (size_t i = 0; i < n; i++) {
        wsp_ggml_backend_dev_t dev = wsp_ggml_backend_dev_get(i);
        enum wsp_ggml_backend_dev_type t = wsp_ggml_backend_dev_type(dev);
        if (t == WSP_GGML_BACKEND_DEVICE_TYPE_GPU || t == WSP_GGML_BACKEND_DEVICE_TYPE_IGPU) {
            return wsp_ggml_backend_dev_description(dev);
        }
    }
    return NULL;
}
*/
import "C"

import "runtime"

// Backend reports, in one line, what whisper is going to compute on. It is
// logged at startup because it is the first thing to check when partial text
// is missing or the final result is slow: a CPU-only build, or a machine
// without a usable Vulkan driver, is 50-100x slower than a GPU on the large
// model and looks "broken" rather than slow.
//
// threads is the configured cap; 0 means the binding's default of every core.
func Backend(threads int) string {
	if desc := C.sussurro_asr_gpu_description(); desc != nil {
		return "GPU: " + C.GoString(desc)
	}
	if threads <= 0 {
		threads = runtime.NumCPU()
	}
	return "CPU (" + itoa(threads) + " threads)"
}

// itoa avoids importing strconv into a file whose only other import is C.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
