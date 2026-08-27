// Package llmipc defines the bounded protocol between Sussurro and its LLM
// helper process. It contains no native dependencies.
package llmipc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	Version      = 1
	MaxFrameSize = 8 << 20

	MaxContextSize = 1 << 20
	MaxGPULayers   = 100_000
	MaxTokens      = 1 << 20
	MaxThreads     = 4096

	CommandInit     = "init"
	CommandPredict  = "predict"
	CommandShutdown = "shutdown"
)

var ErrFrameTooLarge = errors.New("llm ipc frame exceeds 8 MiB")

type Request struct {
	Version int             `json:"version"`
	ID      string          `json:"id"`
	Command string          `json:"command"`
	Init    *InitRequest    `json:"init,omitempty"`
	Predict *PredictRequest `json:"predict,omitempty"`
}

type InitRequest struct {
	ModelPath   string `json:"model_path"`
	ContextSize int    `json:"context_size"`
	GPULayers   int    `json:"gpu_layers"`
	Debug       bool   `json:"debug,omitempty"`
}

type PredictRequest struct {
	Prompt  string         `json:"prompt"`
	Options PredictOptions `json:"options"`
}

type PredictOptions struct {
	Tokens      int      `json:"tokens"`
	Threads     int      `json:"threads"`
	Temperature float32  `json:"temperature"`
	TopP        float32  `json:"top_p"`
	StopWords   []string `json:"stop_words,omitempty"`
	Debug       bool     `json:"debug,omitempty"`
}

type Response struct {
	Version int              `json:"version"`
	ID      string           `json:"id"`
	Result  *PredictResponse `json:"result,omitempty"`
	Error   *Error           `json:"error,omitempty"`
}

type PredictResponse struct {
	Text string `json:"text"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) AsError() error {
	if e == nil {
		return nil
	}
	return fmt.Errorf("llm helper %s: %s", e.Code, e.Message)
}

// WriteFrame writes one JSON object followed by a newline. JSON escapes
// embedded newlines, so one physical line always equals one protocol frame.
func WriteFrame(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode llm ipc frame: %w", err)
	}
	if len(data) > MaxFrameSize {
		return ErrFrameTooLarge
	}
	data = append(data, '\n')
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return fmt.Errorf("write llm ipc frame: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("write llm ipc frame: %w", io.ErrShortWrite)
		}
		data = data[n:]
	}
	return nil
}

// ReadFrame reads exactly one bounded NDJSON frame. The limit is checked
// before unmarshalling so a broken or hostile peer cannot force an unbounded
// allocation.
func ReadFrame(r *bufio.Reader, value any) error {
	var data []byte
	for {
		fragment, err := r.ReadSlice('\n')
		if len(data)+len(fragment) > MaxFrameSize+1 {
			return ErrFrameTooLarge
		}
		data = append(data, fragment...)
		if err == nil {
			break
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			if errors.Is(err, io.EOF) && len(data) > 0 {
				break
			}
			return err
		}
	}

	data = bytes.TrimSuffix(data, []byte{'\n'})
	if len(data) > MaxFrameSize {
		return ErrFrameTooLarge
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("empty llm ipc frame")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("decode llm ipc frame: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("decode llm ipc frame: multiple JSON values")
		}
		return fmt.Errorf("decode llm ipc frame: %w", err)
	}
	return nil
}

func ValidateRequest(req Request) error {
	if req.Version != Version {
		return fmt.Errorf("unsupported protocol version %d", req.Version)
	}
	if req.ID == "" {
		return errors.New("missing request id")
	}
	switch req.Command {
	case CommandInit:
		if req.Init == nil || req.Predict != nil {
			return errors.New("init command requires only an init payload")
		}
		if req.Init.ModelPath == "" {
			return errors.New("init model_path must not be empty")
		}
		if req.Init.ContextSize < 1 || req.Init.ContextSize > MaxContextSize {
			return fmt.Errorf("init context_size must be between 1 and %d", MaxContextSize)
		}
		if req.Init.GPULayers < 0 || req.Init.GPULayers > MaxGPULayers {
			return fmt.Errorf("init gpu_layers must be between 0 and %d", MaxGPULayers)
		}
	case CommandPredict:
		if req.Predict == nil || req.Init != nil {
			return errors.New("predict command requires only a predict payload")
		}
		if req.Predict.Prompt == "" {
			return errors.New("predict prompt must not be empty")
		}
		options := req.Predict.Options
		if options.Tokens < 1 || options.Tokens > MaxTokens {
			return fmt.Errorf("predict tokens must be between 1 and %d", MaxTokens)
		}
		if options.Threads < 1 || options.Threads > MaxThreads {
			return fmt.Errorf("predict threads must be between 1 and %d", MaxThreads)
		}
		if options.Temperature < 0 || options.Temperature > 2 {
			return errors.New("predict temperature must be between 0 and 2")
		}
		if options.TopP <= 0 || options.TopP > 1 {
			return errors.New("predict top_p must be greater than 0 and at most 1")
		}
	case CommandShutdown:
		if req.Init != nil || req.Predict != nil {
			return errors.New("shutdown command cannot have a payload")
		}
	default:
		return fmt.Errorf("unknown command %q", req.Command)
	}
	return nil
}

func ValidateResponse(resp Response, requestID string) error {
	if resp.Version != Version {
		return fmt.Errorf("unsupported protocol version %d", resp.Version)
	}
	if resp.ID != requestID {
		return fmt.Errorf("response id %q does not match request id %q", resp.ID, requestID)
	}
	if resp.Error != nil && resp.Result != nil {
		return errors.New("response contains both result and error")
	}
	return nil
}
