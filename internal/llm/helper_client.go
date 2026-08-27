package llm

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/aploide/sussurro/internal/llmipc"
)

const helperOverrideEnv = "SUSSURRO_LLM_HELPER"

var (
	helperInitTimeout     = 2 * time.Minute
	helperPredictTimeout  = 30 * time.Second
	helperShutdownTimeout = 5 * time.Second
)

type helperConfig struct {
	path        string
	modelPath   string
	contextSize int
	gpuLayers   int
	debug       bool
}

type helperProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	done   chan struct{}

	terminateOnce sync.Once
}

type helperClient struct {
	// mu serializes initialization, restart, and prediction requests. Close does
	// not wait for it: it marks the client closed and terminates the active child,
	// which interrupts a request blocked in stdio.
	mu sync.Mutex

	stateMu sync.Mutex
	child   *helperProcess
	closed  bool

	config helperConfig
	nextID uint64
}

func startHelper(modelPath string, contextSize, gpuLayers int, debug bool) (*helperClient, error) {
	path, err := helperPath()
	if err != nil {
		return nil, err
	}
	client := &helperClient{config: helperConfig{
		path:        path,
		modelPath:   modelPath,
		contextSize: contextSize,
		gpuLayers:   gpuLayers,
		debug:       debug,
	}}

	client.mu.Lock()
	err = client.ensureStartedLocked()
	client.mu.Unlock()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("initialize llm helper: %w", err)
	}
	return client, nil
}

func helperBinaryName() string {
	if runtime.GOOS == "windows" {
		return "sussurro-llm-helper.exe"
	}
	return "sussurro-llm-helper"
}

func helperPath() (string, error) {
	if override := os.Getenv(helperOverrideEnv); override != "" {
		return override, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate sussurro executable: %w", err)
	}
	return resolveHelperPath("", executable, exec.LookPath)
}

func resolveHelperPath(override, executable string, lookPath func(string) (string, error)) (string, error) {
	if override != "" {
		return override, nil
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("resolve sussurro executable %q: %w", executable, err)
	}
	sibling := filepath.Join(filepath.Dir(resolved), helperBinaryName())
	if _, err := os.Stat(sibling); err == nil || !errors.Is(err, os.ErrNotExist) {
		return sibling, nil
	}
	if found, err := lookPath(helperBinaryName()); err == nil {
		return found, nil
	}
	// Preserve the useful sibling path in the eventual exec error.
	return sibling, nil
}

func launchHelper(config helperConfig) (*helperProcess, error) {
	cmd := exec.Command(config.path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open llm helper stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open llm helper stdout: %w", err)
	}
	if config.debug {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = io.Discard
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start llm helper %q: %w", config.path, err)
	}

	process := &helperProcess{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReaderSize(stdout, 64*1024),
		done:   make(chan struct{}),
	}
	go func() {
		_ = cmd.Wait()
		close(process.done)
	}()
	return process, nil
}

func (c *helperClient) ensureStartedLocked() error {
	c.stateMu.Lock()
	if c.closed {
		c.stateMu.Unlock()
		return errors.New("llm helper is closed")
	}
	if c.child != nil {
		select {
		case <-c.child.done:
			c.child = nil
		default:
			c.stateMu.Unlock()
			return nil
		}
	}
	process, err := launchHelper(c.config)
	if err != nil {
		c.stateMu.Unlock()
		return err
	}
	c.child = process
	c.stateMu.Unlock()

	request := llmipc.Request{
		Command: llmipc.CommandInit,
		Init: &llmipc.InitRequest{
			ModelPath:   c.config.modelPath,
			ContextSize: c.config.contextSize,
			GPULayers:   c.config.gpuLayers,
			Debug:       c.config.debug,
		},
	}
	_, err, _ = c.callProcessLocked(process, request, helperInitTimeout)
	if err != nil {
		c.retire(process)
		return err
	}
	return nil
}

func (c *helperClient) Predict(prompt string, options llmipc.PredictOptions) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureStartedLocked(); err != nil {
		return "", err
	}
	process := c.currentProcess()
	response, err, fatal := c.callProcessLocked(process, llmipc.Request{
		Command: llmipc.CommandPredict,
		Predict: &llmipc.PredictRequest{
			Prompt:  prompt,
			Options: options,
		},
	}, helperPredictTimeout)
	if fatal {
		c.retire(process)
	}
	if err != nil {
		return "", err
	}
	if response.Result == nil {
		c.retire(process)
		return "", errors.New("llm helper returned no prediction result")
	}
	return response.Result.Text, nil
}

func (c *helperClient) callProcessLocked(process *helperProcess, request llmipc.Request, timeout time.Duration) (llmipc.Response, error, bool) {
	if process == nil {
		return llmipc.Response{}, errors.New("llm helper is not running"), true
	}

	c.nextID++
	request.Version = llmipc.Version
	request.ID = strconv.FormatUint(c.nextID, 10)

	type callResult struct {
		response llmipc.Response
		err      error
		fatal    bool
	}
	done := make(chan callResult, 1)
	go func() {
		if err := llmipc.WriteFrame(process.stdin, request); err != nil {
			done <- callResult{err: err, fatal: true}
			return
		}
		var response llmipc.Response
		if err := llmipc.ReadFrame(process.stdout, &response); err != nil {
			done <- callResult{err: err, fatal: true}
			return
		}
		if err := llmipc.ValidateResponse(response, request.ID); err != nil {
			done <- callResult{err: err, fatal: true}
			return
		}
		if response.Error != nil {
			done <- callResult{err: response.Error.AsError()}
			return
		}
		if request.Command == llmipc.CommandPredict && response.Result == nil {
			done <- callResult{err: errors.New("llm helper returned no prediction result"), fatal: true}
			return
		}
		done <- callResult{response: response}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-done:
		return result.response, result.err, result.fatal
	case <-timer.C:
		return llmipc.Response{}, fmt.Errorf("llm helper %s timed out after %s", request.Command, timeout), true
	}
}

func (c *helperClient) currentProcess() *helperProcess {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.child
}

func (c *helperClient) retire(process *helperProcess) {
	if process == nil {
		return
	}
	c.stateMu.Lock()
	if c.child == process {
		c.child = nil
	}
	c.stateMu.Unlock()
	process.terminate()
}

func (c *helperClient) Close() {
	c.stateMu.Lock()
	if c.closed {
		c.stateMu.Unlock()
		return
	}
	c.closed = true
	process := c.child
	c.stateMu.Unlock()

	if process == nil {
		return
	}
	if c.mu.TryLock() {
		_, _, _ = c.callProcessLocked(process, llmipc.Request{Command: llmipc.CommandShutdown}, helperShutdownTimeout)
		c.mu.Unlock()
	}
	// If a request owns mu, killing the child interrupts its blocked stdio.
	c.retire(process)
}

func (p *helperProcess) terminate() {
	p.terminateOnce.Do(func() {
		_ = p.stdin.Close()
		select {
		case <-p.done:
			return
		case <-time.After(250 * time.Millisecond):
			_ = p.cmd.Process.Kill()
			<-p.done
		}
	})
}
