package llm

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aploide/sussurro/internal/llmipc"
)

const (
	fakeHelperModeEnv   = "SUSSURRO_TEST_LLM_HELPER_MODE"
	fakeHelperMarkerEnv = "SUSSURRO_TEST_LLM_HELPER_MARKER"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeHelperModeEnv); mode != "" {
		os.Exit(runFakeHelper(mode))
	}
	os.Exit(m.Run())
}

func runFakeHelper(mode string) int {
	reader := bufio.NewReader(os.Stdin)
	var request llmipc.Request
	if err := llmipc.ReadFrame(reader, &request); err != nil {
		return 2
	}
	if mode == "init-error" {
		_ = llmipc.WriteFrame(os.Stdout, llmipc.Response{
			Version: llmipc.Version,
			ID:      request.ID,
			Error:   &llmipc.Error{Code: "model_init_failed", Message: "fake init failure"},
		})
		return 0
	}
	if err := llmipc.WriteFrame(os.Stdout, llmipc.Response{Version: llmipc.Version, ID: request.ID}); err != nil {
		return 2
	}

	for {
		request = llmipc.Request{}
		if err := llmipc.ReadFrame(reader, &request); err != nil {
			if errors.Is(err, io.EOF) {
				return 0
			}
			return 2
		}
		switch request.Command {
		case llmipc.CommandPredict:
			switch mode {
			case "timeout":
				timer := time.NewTimer(time.Hour)
				<-timer.C
			case "hang":
				_ = os.WriteFile(os.Getenv(fakeHelperMarkerEnv), []byte("started"), 0o600)
				timer := time.NewTimer(time.Hour)
				<-timer.C
			case "predict-error":
				_ = llmipc.WriteFrame(os.Stdout, llmipc.Response{
					Version: llmipc.Version,
					ID:      request.ID,
					Error:   &llmipc.Error{Code: "prediction_failed", Message: "fake prediction failure"},
				})
			case "bad-id":
				_ = llmipc.WriteFrame(os.Stdout, llmipc.Response{Version: llmipc.Version, ID: "wrong"})
			case "recover-on-restart":
				marker := os.Getenv(fakeHelperMarkerEnv)
				if _, err := os.Stat(marker); errors.Is(err, os.ErrNotExist) {
					_ = os.WriteFile(marker, []byte(request.Predict.Prompt), 0o600)
					_ = llmipc.WriteFrame(os.Stdout, llmipc.Response{Version: llmipc.Version, ID: "wrong"})
					return 0
				}
				_ = llmipc.WriteFrame(os.Stdout, llmipc.Response{
					Version: llmipc.Version,
					ID:      request.ID,
					Result:  &llmipc.PredictResponse{Text: request.Predict.Prompt},
				})
			default:
				_ = llmipc.WriteFrame(os.Stdout, llmipc.Response{
					Version: llmipc.Version,
					ID:      request.ID,
					Result:  &llmipc.PredictResponse{Text: "fake prediction"},
				})
			}
		case llmipc.CommandShutdown:
			_ = llmipc.WriteFrame(os.Stdout, llmipc.Response{Version: llmipc.Version, ID: request.ID})
			return 0
		}
	}
}

func useFakeHelper(t *testing.T, mode string) {
	t.Helper()
	t.Setenv(helperOverrideEnv, os.Args[0])
	t.Setenv(fakeHelperModeEnv, mode)
	t.Setenv(fakeHelperMarkerEnv, "")
}

func validClientOptions() llmipc.PredictOptions {
	return llmipc.PredictOptions{Tokens: 10, Threads: 2, Temperature: 0.1, TopP: 0.9}
}

func TestHelperPredictionTimeoutIsThirtySeconds(t *testing.T) {
	if helperPredictTimeout != 30*time.Second {
		t.Fatalf("helperPredictTimeout = %s, want 30s", helperPredictTimeout)
	}
}

func TestHelperClientInitializesPredictsAndShutsDown(t *testing.T) {
	useFakeHelper(t, "normal")
	client, err := startHelper("fake-model.gguf", 2048, 99, true)
	if err != nil {
		t.Fatalf("startHelper() error = %v", err)
	}
	process := client.currentProcess()

	got, err := client.Predict("prompt", validClientOptions())
	if err != nil {
		t.Fatalf("Predict() error = %v", err)
	}
	if got != "fake prediction" {
		t.Errorf("Predict() = %q, want fake prediction", got)
	}

	client.Close()
	if process.cmd.ProcessState == nil {
		t.Error("Close() did not reap the helper process")
	}
}

func TestCloseInterruptsHangingPredictionAndReapsChild(t *testing.T) {
	useFakeHelper(t, "hang")
	marker := filepath.Join(t.TempDir(), "prediction-started")
	t.Setenv(fakeHelperMarkerEnv, marker)
	client, err := startHelper("fake-model.gguf", 1024, 0, true)
	if err != nil {
		t.Fatalf("startHelper() error = %v", err)
	}
	process := client.currentProcess()

	predictionDone := make(chan error, 1)
	go func() {
		_, err := client.Predict("hang", validClientOptions())
		predictionDone <- err
	}()
	waitForFile(t, marker)

	started := time.Now()
	client.Close()
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Close() took %s, want under 1s", elapsed)
	}
	select {
	case err := <-predictionDone:
		if err == nil {
			t.Error("hanging Predict() returned no error after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("hanging Predict() did not return promptly after Close")
	}
	if process.cmd.ProcessState == nil {
		t.Error("Close() did not reap the hanging helper")
	}
}

func TestFatalFailureRestartsOnNextPredictionWithoutRetry(t *testing.T) {
	useFakeHelper(t, "recover-on-restart")
	marker := filepath.Join(t.TempDir(), "failed-request")
	t.Setenv(fakeHelperMarkerEnv, marker)
	client, err := startHelper("fake-model.gguf", 1024, 0, true)
	if err != nil {
		t.Fatalf("startHelper() error = %v", err)
	}
	defer client.Close()
	firstProcess := client.currentProcess()

	if _, err := client.Predict("first request", validClientOptions()); err == nil {
		t.Fatal("first Predict() succeeded, want fatal transport error")
	}
	failedPrompt, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(failedPrompt) != "first request" {
		t.Fatalf("first helper received %q", failedPrompt)
	}
	if firstProcess.cmd.ProcessState == nil {
		t.Error("failed helper was not reaped")
	}

	got, err := client.Predict("second request", validClientOptions())
	if err != nil {
		t.Fatalf("second Predict() error = %v", err)
	}
	if got != "second request" {
		t.Fatalf("second Predict() = %q, want proof only the second request ran", got)
	}
	if client.currentProcess() == firstProcess {
		t.Error("second prediction did not start a fresh helper")
	}
}

func TestNewEngineReportsHelperInitializationError(t *testing.T) {
	useFakeHelper(t, "init-error")
	_, err := NewEngine(tempModel(t), 2, 1024, 0, true)
	if err == nil || !strings.Contains(err.Error(), "fake init failure") {
		t.Fatalf("NewEngine() error = %v, want structured helper initialization error", err)
	}
}

func TestCleanupPreservesRecognizedTextWhenHelperPredictionFails(t *testing.T) {
	useFakeHelper(t, "predict-error")
	engine, err := NewEngine(tempModel(t), 2, 1024, 0, true)
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	defer engine.Close()
	process := engine.model.(*helperClient).currentProcess()

	const recognized = "The recognized words must survive intact."
	got, err := engine.CleanupText(recognized)
	if err != nil {
		t.Fatalf("CleanupText() error = %v", err)
	}
	if got != recognized {
		t.Errorf("CleanupText() = %q, want recognized text %q", got, recognized)
	}
	if engine.model.(*helperClient).currentProcess() != process {
		t.Error("a structured prediction error unnecessarily replaced the helper")
	}
}

func TestHelperTimeoutKillsAndReapsChild(t *testing.T) {
	useFakeHelper(t, "timeout")
	oldTimeout := helperPredictTimeout
	helperPredictTimeout = 50 * time.Millisecond
	t.Cleanup(func() { helperPredictTimeout = oldTimeout })

	client, err := startHelper("fake-model.gguf", 1024, 0, true)
	if err != nil {
		t.Fatalf("startHelper() error = %v", err)
	}
	process := client.currentProcess()
	_, err = client.Predict("prompt", validClientOptions())
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Predict() error = %v, want timeout", err)
	}
	if process.cmd.ProcessState == nil {
		t.Error("timed-out helper process was not reaped")
	}
}

func TestInvalidHelperResponseStopsAndReapsChild(t *testing.T) {
	useFakeHelper(t, "bad-id")
	client, err := startHelper("fake-model.gguf", 1024, 0, true)
	if err != nil {
		t.Fatalf("startHelper() error = %v", err)
	}
	process := client.currentProcess()
	_, err = client.Predict("prompt", validClientOptions())
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("Predict() error = %v, want response-id error", err)
	}
	if process.cmd.ProcessState == nil {
		t.Error("broken helper process was not reaped")
	}
}

func TestResolveHelperPathHonorsOverride(t *testing.T) {
	got, err := resolveHelperPath("/custom/helper", "/missing/executable", execLookupMustNotRun(t))
	if err != nil || got != "/custom/helper" {
		t.Fatalf("resolveHelperPath() = %q, %v", got, err)
	}
}

func TestResolveHelperPathUsesRealExecutableSiblingThroughSymlink(t *testing.T) {
	realDir := t.TempDir()
	executable := filepath.Join(realDir, "sussurro")
	helper := filepath.Join(realDir, helperBinaryName())
	if err := os.WriteFile(executable, []byte("app"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, []byte("helper"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "sussurro-link")
	if err := os.Symlink(executable, link); err != nil {
		t.Fatal(err)
	}

	got, err := resolveHelperPath("", link, execLookupMustNotRun(t))
	if err != nil {
		t.Fatal(err)
	}
	if got != helper {
		t.Errorf("resolveHelperPath() = %q, want resolved sibling %q", got, helper)
	}
}

func TestResolveHelperPathFallsBackToPATHAfterMissingSibling(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "sussurro")
	if err := os.WriteFile(executable, []byte("app"), 0o700); err != nil {
		t.Fatal(err)
	}
	const pathHelper = "/from/path/sussurro-llm-helper"
	got, err := resolveHelperPath("", executable, func(name string) (string, error) {
		if name != helperBinaryName() {
			t.Fatalf("LookPath(%q)", name)
		}
		return pathHelper, nil
	})
	if err != nil || got != pathHelper {
		t.Fatalf("resolveHelperPath() = %q, %v", got, err)
	}
}

func execLookupMustNotRun(t *testing.T) func(string) (string, error) {
	t.Helper()
	return func(string) (string, error) {
		t.Fatal("PATH lookup ran unexpectedly")
		return "", errors.New("unreachable")
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func tempModel(t *testing.T) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "model-*.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return file.Name()
}
