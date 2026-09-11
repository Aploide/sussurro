package llmipc

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	request := Request{
		Version: Version,
		ID:      "42",
		Command: CommandPredict,
		Predict: &PredictRequest{
			Prompt: "first line\nsecond line",
			Options: PredictOptions{
				Tokens:      512,
				Threads:     4,
				Temperature: 0.1,
				TopP:        0.9,
				StopWords:   []string{"<|im_end|>"},
			},
		},
	}

	var encoded bytes.Buffer
	if err := WriteFrame(&encoded, request); err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}
	if lines := strings.Count(encoded.String(), "\n"); lines != 1 {
		t.Fatalf("encoded frame has %d physical newlines, want 1", lines)
	}

	var decoded Request
	if err := ReadFrame(bufio.NewReader(&encoded), &decoded); err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	if decoded.Predict == nil || decoded.Predict.Prompt != request.Predict.Prompt {
		t.Fatalf("decoded prompt = %#v, want %q", decoded.Predict, request.Predict.Prompt)
	}
	if err := ValidateRequest(decoded); err != nil {
		t.Fatalf("ValidateRequest() error = %v", err)
	}
}

func TestReadFrameRejectsOversizedInput(t *testing.T) {
	data := strings.Repeat("x", MaxFrameSize+1) + "\n"
	var decoded Request
	err := ReadFrame(bufio.NewReaderSize(strings.NewReader(data), 1024), &decoded)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("ReadFrame() error = %v, want ErrFrameTooLarge", err)
	}
}

func TestWriteFrameRejectsOversizedOutput(t *testing.T) {
	request := Request{
		Version: Version,
		ID:      "1",
		Command: CommandPredict,
		Predict: &PredictRequest{Prompt: strings.Repeat("x", MaxFrameSize)},
	}
	if err := WriteFrame(&bytes.Buffer{}, request); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("WriteFrame() error = %v, want ErrFrameTooLarge", err)
	}
}

func TestReadFrameRejectsUnknownFields(t *testing.T) {
	data := `{"version":1,"id":"1","command":"shutdown","surprise":true}` + "\n"
	var request Request
	err := ReadFrame(bufio.NewReader(strings.NewReader(data)), &request)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("ReadFrame() error = %v, want unknown-field rejection", err)
	}
}

func TestProtocolValidationRejectsInvalidNumericRanges(t *testing.T) {
	validInit := func() Request {
		return Request{Version: Version, ID: "1", Command: CommandInit, Init: &InitRequest{
			ModelPath: "model.gguf", ContextSize: 4096, GPULayers: 99,
		}}
	}
	validPredict := func() Request {
		return Request{Version: Version, ID: "1", Command: CommandPredict, Predict: &PredictRequest{
			Prompt: "prompt", Options: PredictOptions{Tokens: 512, Threads: 4, Temperature: 0.1, TopP: 0.9},
		}}
	}

	var invalid []Request
	for _, mutate := range []func(*InitRequest){
		func(v *InitRequest) { v.ModelPath = "" },
		func(v *InitRequest) { v.ContextSize = 0 },
		func(v *InitRequest) { v.ContextSize = MaxContextSize + 1 },
		func(v *InitRequest) { v.GPULayers = -1 },
		func(v *InitRequest) { v.GPULayers = MaxGPULayers + 1 },
	} {
		request := validInit()
		mutate(request.Init)
		invalid = append(invalid, request)
	}
	for _, mutate := range []func(*PredictRequest){
		func(v *PredictRequest) { v.Prompt = "" },
		func(v *PredictRequest) { v.Options.Tokens = 0 },
		func(v *PredictRequest) { v.Options.Tokens = MaxTokens + 1 },
		func(v *PredictRequest) { v.Options.Threads = 0 },
		func(v *PredictRequest) { v.Options.Threads = MaxThreads + 1 },
		func(v *PredictRequest) { v.Options.Temperature = -0.1 },
		func(v *PredictRequest) { v.Options.Temperature = 2.1 },
		func(v *PredictRequest) { v.Options.TopP = 0 },
		func(v *PredictRequest) { v.Options.TopP = 1.1 },
	} {
		request := validPredict()
		mutate(request.Predict)
		invalid = append(invalid, request)
	}

	for _, request := range invalid {
		if err := ValidateRequest(request); err == nil {
			t.Errorf("ValidateRequest(%+v) succeeded, want range error", request)
		}
	}
}

func TestProtocolValidationRejectsVersionAndShapeErrors(t *testing.T) {
	tests := []Request{
		{Version: Version + 1, ID: "1", Command: CommandShutdown},
		{Version: Version, Command: CommandShutdown},
		{Version: Version, ID: "1", Command: CommandPredict},
		{Version: Version, ID: "1", Command: "unknown"},
	}
	for _, request := range tests {
		if err := ValidateRequest(request); err == nil {
			t.Errorf("ValidateRequest(%+v) succeeded, want error", request)
		}
	}

	if err := ValidateResponse(Response{Version: Version, ID: "other"}, "1"); err == nil {
		t.Error("ValidateResponse() accepted a mismatched request id")
	}
}
