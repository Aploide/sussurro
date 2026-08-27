package main

import (
	"bufio"
	"bytes"
	"errors"
	"testing"

	llama "github.com/AshkanYarmoradi/go-llama.cpp"
	"github.com/aploide/sussurro/internal/llmipc"
)

type fakeModel struct {
	output string
	err    error
	prompt string
	calls  int
	freed  bool
}

func (m *fakeModel) Predict(prompt string, _ ...llama.PredictOption) (string, error) {
	m.calls++
	m.prompt = prompt
	return m.output, m.err
}

func (m *fakeModel) Free() { m.freed = true }

func TestServeInitPredictShutdown(t *testing.T) {
	fake := &fakeModel{output: "cleaned text"}
	var initialized llmipc.InitRequest
	factory := func(request llmipc.InitRequest) (model, error) {
		initialized = request
		return fake, nil
	}
	responses := runServer(t, factory,
		validInitRequest("1"),
		validPredictRequest("2", "prompt text"),
		llmipc.Request{Version: llmipc.Version, ID: "3", Command: llmipc.CommandShutdown},
	)

	if len(responses) != 3 {
		t.Fatalf("response count = %d, want 3", len(responses))
	}
	if responses[0].Error != nil {
		t.Fatalf("init error = %v", responses[0].Error)
	}
	if responses[1].Result == nil || responses[1].Result.Text != "cleaned text" {
		t.Fatalf("predict response = %+v", responses[1])
	}
	if initialized.ModelPath != "model.gguf" || initialized.ContextSize != 4096 || initialized.GPULayers != 99 {
		t.Errorf("init request = %+v", initialized)
	}
	if fake.calls != 1 || fake.prompt != "prompt text" {
		t.Errorf("model prediction calls=%d prompt=%q", fake.calls, fake.prompt)
	}
	if !fake.freed {
		t.Error("shutdown did not free the model")
	}
}

func TestServeReturnsStructuredErrors(t *testing.T) {
	t.Run("initialization", func(t *testing.T) {
		responses := runServer(t, func(llmipc.InitRequest) (model, error) {
			return nil, errors.New("cannot load model")
		},
			validInitRequest("1"),
			llmipc.Request{Version: llmipc.Version, ID: "2", Command: llmipc.CommandShutdown},
		)
		assertErrorCode(t, responses[0], "model_init_failed")
	})

	t.Run("prediction before initialization", func(t *testing.T) {
		responses := runServer(t, func(llmipc.InitRequest) (model, error) {
			t.Fatal("factory called unexpectedly")
			return nil, nil
		},
			validPredictRequest("1", "prompt"),
			llmipc.Request{Version: llmipc.Version, ID: "2", Command: llmipc.CommandShutdown},
		)
		assertErrorCode(t, responses[0], "not_initialized")
	})

	t.Run("prediction failure", func(t *testing.T) {
		failed := &fakeModel{err: errors.New("inference failed")}
		responses := runServer(t, func(llmipc.InitRequest) (model, error) { return failed, nil },
			validInitRequest("1"),
			validPredictRequest("2", "prompt"),
			llmipc.Request{Version: llmipc.Version, ID: "3", Command: llmipc.CommandShutdown},
		)
		assertErrorCode(t, responses[1], "prediction_failed")
	})

	t.Run("invalid request", func(t *testing.T) {
		invalid := validPredictRequest("1", "prompt")
		invalid.Predict.Options.Threads = 0
		responses := runServer(t, func(llmipc.InitRequest) (model, error) { return &fakeModel{}, nil },
			invalid,
			llmipc.Request{Version: llmipc.Version, ID: "2", Command: llmipc.CommandShutdown},
		)
		assertErrorCode(t, responses[0], "invalid_request")
	})
}

func runServer(t *testing.T, factory modelFactory, requests ...llmipc.Request) []llmipc.Response {
	t.Helper()
	var input bytes.Buffer
	for _, request := range requests {
		if err := llmipc.WriteFrame(&input, request); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	if err := serve(&input, &output, factory); err != nil {
		t.Fatalf("serve() error = %v", err)
	}

	reader := bufio.NewReader(&output)
	responses := make([]llmipc.Response, 0, len(requests))
	for range requests {
		var response llmipc.Response
		if err := llmipc.ReadFrame(reader, &response); err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	return responses
}

func validInitRequest(id string) llmipc.Request {
	return llmipc.Request{
		Version: llmipc.Version,
		ID:      id,
		Command: llmipc.CommandInit,
		Init: &llmipc.InitRequest{
			ModelPath:   "model.gguf",
			ContextSize: 4096,
			GPULayers:   99,
		},
	}
}

func validPredictRequest(id, prompt string) llmipc.Request {
	return llmipc.Request{
		Version: llmipc.Version,
		ID:      id,
		Command: llmipc.CommandPredict,
		Predict: &llmipc.PredictRequest{
			Prompt: prompt,
			Options: llmipc.PredictOptions{
				Tokens:      512,
				Threads:     4,
				Temperature: 0.1,
				TopP:        0.9,
			},
		},
	}
}

func assertErrorCode(t *testing.T, response llmipc.Response, code string) {
	t.Helper()
	if response.Error == nil || response.Error.Code != code {
		t.Fatalf("response error = %+v, want code %q", response.Error, code)
	}
}
