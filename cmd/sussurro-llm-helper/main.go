package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"

	llama "github.com/AshkanYarmoradi/go-llama.cpp"
	"github.com/aploide/sussurro/internal/llmipc"
)

type model interface {
	Predict(string, ...llama.PredictOption) (string, error)
	Free()
}

type modelFactory func(llmipc.InitRequest) (model, error)

func main() {
	if err := serve(os.Stdin, os.Stdout, newNativeModel); err != nil {
		fmt.Fprintf(os.Stderr, "sussurro-llm-helper: %v\n", err)
		os.Exit(1)
	}
}

func newNativeModel(request llmipc.InitRequest) (model, error) {
	return llama.New(
		request.ModelPath,
		llama.SetContext(request.ContextSize),
		llama.SetGPULayers(request.GPULayers),
	)
}

func serve(input io.Reader, output io.Writer, newModel modelFactory) error {
	reader := bufio.NewReaderSize(input, 64*1024)
	var loaded model
	defer func() {
		if loaded != nil {
			loaded.Free()
		}
	}()

	for {
		var request llmipc.Request
		if err := llmipc.ReadFrame(reader, &request); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if err := llmipc.ValidateRequest(request); err != nil {
			if writeErr := writeError(output, request.ID, "invalid_request", err); writeErr != nil {
				return writeErr
			}
			continue
		}

		switch request.Command {
		case llmipc.CommandInit:
			if loaded != nil {
				if err := writeError(output, request.ID, "already_initialized", errors.New("model is already initialized")); err != nil {
					return err
				}
				continue
			}
			initialized, err := newModel(*request.Init)
			if err == nil && initialized == nil {
				err = errors.New("model factory returned no model")
			}
			if err != nil {
				if err := writeError(output, request.ID, "model_init_failed", err); err != nil {
					return err
				}
				continue
			}
			loaded = initialized
			if err := llmipc.WriteFrame(output, llmipc.Response{Version: llmipc.Version, ID: request.ID}); err != nil {
				return err
			}

		case llmipc.CommandPredict:
			if loaded == nil {
				if err := writeError(output, request.ID, "not_initialized", errors.New("model is not initialized")); err != nil {
					return err
				}
				continue
			}
			prediction, err := loaded.Predict(request.Predict.Prompt, predictOptions(request.Predict.Options)...)
			if err != nil {
				if err := writeError(output, request.ID, "prediction_failed", err); err != nil {
					return err
				}
				continue
			}
			if err := llmipc.WriteFrame(output, llmipc.Response{
				Version: llmipc.Version,
				ID:      request.ID,
				Result:  &llmipc.PredictResponse{Text: prediction},
			}); err != nil {
				return err
			}

		case llmipc.CommandShutdown:
			// Release the native model before acknowledging shutdown. The parent
			// can then reap a process that has completed all native cleanup.
			if loaded != nil {
				loaded.Free()
				loaded = nil
			}
			if err := llmipc.WriteFrame(output, llmipc.Response{Version: llmipc.Version, ID: request.ID}); err != nil {
				return err
			}
			return nil
		}
	}
}

func predictOptions(options llmipc.PredictOptions) []llama.PredictOption {
	result := []llama.PredictOption{
		llama.SetTokens(options.Tokens),
		llama.SetThreads(options.Threads),
		llama.SetTemperature(options.Temperature),
		llama.SetTopP(options.TopP),
	}
	if len(options.StopWords) > 0 {
		result = append(result, llama.SetStopWords(options.StopWords...))
	}
	if options.Debug {
		result = append(result, llama.Debug)
	}
	return result
}

func writeError(output io.Writer, id, code string, err error) error {
	return llmipc.WriteFrame(output, llmipc.Response{
		Version: llmipc.Version,
		ID:      id,
		Error: &llmipc.Error{
			Code:    code,
			Message: err.Error(),
		},
	})
}
