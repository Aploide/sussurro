package ui

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"

	"github.com/aploide/sussurro/internal/config"
	"github.com/aploide/sussurro/internal/setup"
	"github.com/aploide/sussurro/internal/version"
)

// modelInfo describes a model for the settings UI.
type modelInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"desc"`
	Size        string `json:"size"`
	Installed   bool   `json:"installed"`
	Active      bool   `json:"active"`
	Type        string `json:"type"` // "whisper" or "llm"
}

// initialData is returned by getInitialData().
type initialData struct {
	Platform         string      `json:"platform"`
	Version          string      `json:"version"`
	Models           []modelInfo `json:"models"`
	PushToTalkHotkey string      `json:"pushToTalkHotkey"`
	ToggleHotkey     string      `json:"toggleHotkey"`
	IsWayland        bool        `json:"isWayland"`
	Language         string      `json:"language"`
	LowercaseOutput  bool        `json:"lowercaseOutput"`
	SkipLLMCleanup   bool        `json:"skipLLMCleanup"`
	// Workflow carries the review controls and this host's capabilities.
	Workflow workflowSettings `json:"workflow"`
}

// bindBridge attaches all Go↔JS bridge functions to the webview.
func bindBridge(sw *settingsWindow) {
	mgr := sw.mgr

	sw.w.Bind("getInitialData", func() (result string) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in getInitialData", "error", r)
				result = `{"error":"internal error"}`
			}
		}()
		data := buildInitialData(mgr)
		b, _ := json.Marshal(data)
		return string(b)
	})

	// One entry point for every workflow setting: the key names the field, so
	// six controls need one binding rather than six near-identical ones.
	sw.w.Bind("saveWorkflowSetting", func(key, value string) (result string) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in saveWorkflowSetting", "error", r)
				result = fmt.Sprintf("error: panic: %v", r)
			}
		}()
		if err := saveWorkflowSetting(mgr.cfg, key, value); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "ok"
	})

	// One binding per call, named by which it is. The previous design had a
	// single trigger plus a mode, which made the behaviour a property of the
	// binding and so allowed only one at a time.
	saveBinding := func(name string, apply func(string)) func(string) string {
		return func(trigger string) (result string) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("panic saving hotkey", "binding", name, "error", r)
					result = fmt.Sprintf("error: panic: %v", r)
				}
			}()
			if err := config.SaveHotkeyBinding(name, trigger); err != nil {
				return fmt.Sprintf("error: %v", err)
			}
			apply(trigger)
			// Re-register immediately so the change needs no restart.
			go mgr.UpdateHotkeyBindings(mgr.cfg.Hotkey.PushToTalk, mgr.cfg.Hotkey.Toggle)
			return "ok"
		}
	}

	sw.w.Bind("savePushToTalkHotkey", saveBinding("push_to_talk", func(t string) {
		mgr.cfg.Hotkey.PushToTalk = t
	}))
	sw.w.Bind("saveToggleHotkey", saveBinding("toggle", func(t string) {
		mgr.cfg.Hotkey.Toggle = t
	}))

	sw.w.Bind("saveLanguage", func(lang string) (result string) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in saveLanguage", "error", r)
				result = fmt.Sprintf("error: panic: %v", r)
			}
		}()
		if err := config.SaveLanguage(mgr.cfg, lang); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		mgr.cfg.Models.ASR.Language = lang
		return "ok"
	})

	sw.w.Bind("saveLowercaseOutput", func(enabled bool) (result string) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in saveLowercaseOutput", "error", r)
				result = fmt.Sprintf("error: panic: %v", r)
			}
		}()
		if err := config.SaveLowercaseOutput(mgr.cfg, enabled); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		mgr.cfg.App.LowercaseOutput = enabled
		mgr.applyLowercaseOutput(enabled)
		return "ok"
	})

	sw.w.Bind("saveSkipLLMCleanup", func(enabled bool) (result string) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in saveSkipLLMCleanup", "error", r)
				result = fmt.Sprintf("error: panic: %v", r)
			}
		}()
		if err := config.SaveSkipLLMCleanup(mgr.cfg, enabled); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		mgr.cfg.App.SkipLLMCleanup = enabled
		mgr.applySkipLLMCleanup(enabled)
		return "ok"
	})

	sw.w.Bind("downloadModel", func(modelID string) {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("panic in downloadModel goroutine", "error", r)
				}
			}()
			url, dest, name := resolveModelDownload(modelID)
			if url == "" {
				return
			}
			setup.SetProgressCallback(func(_ string, pct float64, _, _ int64) {
				sw.pushDownloadProgress(modelID, pct)
			})
			defer setup.SetProgressCallback(nil)
			if err := setup.DownloadModel(url, dest, name); err != nil {
				sw.w.Dispatch(func() {
					sw.w.Eval(fmt.Sprintf("onDownloadError('%s', '%v')", modelID, err))
				})
				return
			}
			sw.w.Dispatch(func() {
				sw.w.Eval(fmt.Sprintf("onDownloadComplete('%s')", modelID))
			})
		}()
	})

	sw.w.Bind("setActiveModel", func(modelID string) (result string) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in setActiveModel", "error", r)
				result = fmt.Sprintf("error: panic: %v", r)
			}
		}()
		if err := setup.SetActiveModel(modelID); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		// Mirror the new path into the in-memory config so that the next call to
		// getInitialData() returns the updated Active flag and the UI stays correct.
		modelsDir := sussurroModelsDir()
		switch modelID {
		case "whisper-small":
			mgr.cfg.Models.ASR.Path = modelsDir + "/ggml-small.bin"
		case "whisper-large-v3-turbo":
			mgr.cfg.Models.ASR.Path = modelsDir + "/ggml-large-v3-turbo.bin"
		}
		// Config written — the UI shows a restart banner instead of forcing a
		// process restart, so in-flight audio/pipeline goroutines are not disrupted.
		return "ok"
	})

	sw.w.Bind("openURL", func(url string) {
		go func() {
			var cmd *exec.Cmd
			if runtime.GOOS == "darwin" {
				cmd = exec.Command("open", url)
			} else {
				cmd = exec.Command("xdg-open", url)
			}
			if err := cmd.Start(); err != nil {
				slog.Error("openURL failed", "url", url, "error", err)
			}
		}()
	})

	sw.w.Bind("closeSettings", func() {
		sw.Hide()
	})
}

// sussurroModelsDir returns the canonical path to the directory where Sussurro
// stores its model files (~/.sussurro/models).
func sussurroModelsDir() string {
	homeDir, _ := os.UserHomeDir()
	return homeDir + "/.sussurro/models"
}

func buildInitialData(mgr *Manager) initialData {
	modelsDir := sussurroModelsDir()

	whisperSmallPath := modelsDir + "/ggml-small.bin"
	whisperLargePath := modelsDir + "/ggml-large-v3-turbo.bin"
	llmPath := modelsDir + "/qwen3-sussurro-q4_k_m.gguf"

	currentASR := mgr.cfg.Models.ASR.Path
	currentLLM := mgr.cfg.Models.LLM.Path

	models := []modelInfo{
		{
			ID:          "whisper-small",
			Name:        "Whisper Small",
			Description: "Faster, lower memory usage",
			Size:        "~488 MB",
			Installed:   fileExists(whisperSmallPath),
			Active:      currentASR == whisperSmallPath,
			Type:        "whisper",
		},
		{
			ID:          "whisper-large-v3-turbo",
			Name:        "Whisper Large v3 Turbo",
			Description: "Higher accuracy, more memory",
			Size:        "~1.62 GB",
			Installed:   fileExists(whisperLargePath),
			Active:      currentASR == whisperLargePath,
			Type:        "whisper",
		},
		{
			ID:          "qwen3-sussurro",
			Name:        "Qwen 3 Sussurro",
			Description: "Fine-tuned for transcription cleanup",
			Size:        "~1.28 GB",
			Installed:   fileExists(llmPath),
			Active:      currentLLM == llmPath,
			Type:        "llm",
		},
	}

	platform := "LINUX"
	if runtime.GOOS == "darwin" {
		platform = "MACOS"
	}

	isWayland := os.Getenv("WAYLAND_DISPLAY") != "" ||
		os.Getenv("XDG_SESSION_TYPE") == "wayland"
	if isWayland {
		platform += " (WAYLAND)"
	} else if runtime.GOOS == "linux" {
		platform += " (X11)"
	}

	return initialData{
		Platform:         platform,
		Version:          version.Version,
		Models:           models,
		PushToTalkHotkey: mgr.cfg.Hotkey.PushToTalk,
		ToggleHotkey:     mgr.cfg.Hotkey.Toggle,
		IsWayland:        isWayland,
		Language:         mgr.cfg.Models.ASR.Language,
		LowercaseOutput:  mgr.cfg.App.LowercaseOutput,
		SkipLLMCleanup:   mgr.cfg.App.SkipLLMCleanup,
		Workflow:         buildWorkflowSettings(mgr.cfg, hostCapabilities()),
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// resolveModelDownload maps a model ID to its download URL and local path.
func resolveModelDownload(modelID string) (url, dest, name string) {
	modelsDir := sussurroModelsDir()

	switch modelID {
	case "whisper-small":
		return "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.bin",
			modelsDir + "/ggml-small.bin",
			"Whisper Small"
	case "whisper-large-v3-turbo":
		return "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3-turbo.bin",
			modelsDir + "/ggml-large-v3-turbo.bin",
			"Whisper Large v3 Turbo"
	case "qwen3-sussurro":
		return "https://huggingface.co/cesp99/qwen3-sussurro/resolve/main/qwen3-sussurro-q4_k_m.gguf",
			modelsDir + "/qwen3-sussurro-q4_k_m.gguf",
			"Qwen 3 Sussurro"
	}
	return "", "", ""
}
