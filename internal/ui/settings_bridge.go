package ui

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/aploide/sussurro/internal/config"
	"github.com/aploide/sussurro/internal/setup"
	"github.com/aploide/sussurro/internal/version"
)

// modelInfo describes a model for the settings UI.
type modelInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"desc"`
	Size         string `json:"size"`
	Installed    bool   `json:"installed"`
	Active       bool   `json:"active"`
	Downloadable bool   `json:"downloadable"`
	Selectable   bool   `json:"selectable"`
	Type         string `json:"type"` // "whisper" or "llm"
}

// initialData is returned by getInitialData().
type initialData struct {
	Platform         string       `json:"platform"`
	Version          string       `json:"version"`
	Models           []modelInfo  `json:"models"`
	PushToTalkHotkey string       `json:"pushToTalkHotkey"`
	ToggleHotkey     string       `json:"toggleHotkey"`
	EditHotkey       string       `json:"editHotkey"`
	IsWayland        bool         `json:"isWayland"`
	Language         string       `json:"language"`
	LowercaseOutput  bool         `json:"lowercaseOutput"`
	SkipLLMCleanup   bool         `json:"skipLLMCleanup"`
	Dictionary       []string     `json:"dictionary"`
	Theme            config.Theme `json:"theme"`
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

	sw.w.Bind("resizeSettingsWindow", func(cssWidth, cssHeight int) {
		sw.resizeToContent(cssWidth, cssHeight)
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
	saveBinding := func(name string) func(string) string {
		return func(trigger string) (result string) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("panic saving hotkey", "binding", name, "error", r)
					result = fmt.Sprintf("error: panic: %v", r)
				}
			}()
			if err := mgr.SaveHotkeyBinding(name, trigger); err != nil {
				return fmt.Sprintf("error: %v", err)
			}
			return "ok"
		}
	}

	sw.w.Bind("savePushToTalkHotkey", saveBinding("push_to_talk"))
	sw.w.Bind("saveToggleHotkey", saveBinding("toggle"))
	sw.w.Bind("saveEditHotkey", saveBinding("edit"))

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

	sw.w.Bind("saveDictionary", func(encoded string) (result string) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in saveDictionary", "error", r)
				result = fmt.Sprintf("error: panic: %v", r)
			}
		}()
		return saveDictionary(mgr, encoded)
	})

	sw.w.Bind("saveTheme", func(value string) (result string) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in saveTheme", "error", r)
				result = fmt.Sprintf("error: panic: %v", r)
			}
		}()
		return saveTheme(mgr, value)
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
			progress := func(_ string, pct float64, _, _ int64) {
				sw.pushDownloadProgress(modelID, pct)
			}
			if err := setup.DownloadModel(url, dest, name, progress); err != nil {
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
		if err := setup.ActivateModel(mgr.cfg, modelID); err != nil {
			return fmt.Sprintf("error: %v", err)
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

func saveTheme(mgr *Manager, value string) string {
	theme := config.Theme(value)
	if err := config.SaveTheme(mgr.cfg, theme); err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	mgr.cfg.Appearance.Theme = theme
	mgr.applyTheme(theme)
	return "ok"
}

func saveDictionary(mgr *Manager, encoded string) string {
	var terms []string
	if err := json.Unmarshal([]byte(encoded), &terms); err != nil {
		return fmt.Sprintf("error: decode dictionary: %v", err)
	}
	normalized, err := config.SaveDictionary(mgr.cfg, terms)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	mgr.cfg.App.Dictionary = append([]string(nil), normalized...)
	mgr.applyDictionary(normalized)
	return "ok"
}

// sussurroModelsDir returns the canonical path to the directory where Sussurro
// stores its model files (~/.sussurro/models).
func sussurroModelsDir() string {
	homeDir, _ := os.UserHomeDir()
	return homeDir + "/.sussurro/models"
}

func buildInitialData(mgr *Manager) initialData {
	modelsDir := sussurroModelsDir()
	currentASR := mgr.cfg.Models.ASR.Path
	currentLLM := mgr.cfg.Models.LLM.Path

	supported := setup.SupportedModels()
	models := make([]modelInfo, 0, len(supported)+1)
	activeSupportedLLM := false
	for _, model := range supported {
		path := filepath.Join(modelsDir, model.Filename)
		installed := regularFileExists(path)
		active := currentASR == path
		if model.Kind == setup.ModelKindLLM {
			active = currentLLM == path
			activeSupportedLLM = activeSupportedLLM || active
		}
		models = append(models, modelInfo{
			ID:           model.ID,
			Name:         model.Name,
			Description:  model.Description,
			Size:         model.Size,
			Installed:    installed,
			Active:       active,
			Downloadable: true,
			Selectable:   installed,
			Type:         string(model.Kind),
		})
	}

	// Keep an externally configured LLM visible without claiming that every
	// arbitrary GGUF in the models directory supports Sussurro's cleanup prompt.
	if currentLLM != "" && !activeSupportedLLM {
		models = append(models, modelInfo{
			ID:          "configured-llm",
			Name:        filepath.Base(currentLLM),
			Description: "Configured externally; compatibility not verified",
			Size:        "Custom model",
			Installed:   regularFileExists(currentLLM),
			Active:      true,
			Type:        string(setup.ModelKindLLM),
		})
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
		EditHotkey:       mgr.cfg.Hotkey.Edit,
		IsWayland:        isWayland,
		Language:         mgr.cfg.Models.ASR.Language,
		LowercaseOutput:  mgr.cfg.App.LowercaseOutput,
		SkipLLMCleanup:   mgr.cfg.App.SkipLLMCleanup,
		Dictionary:       append([]string(nil), mgr.cfg.App.Dictionary...),
		Theme:            mgr.cfg.Appearance.Theme,
		Workflow:         buildWorkflowSettings(mgr.cfg, hostCapabilities()),
	}
}

func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// resolveModelDownload maps a supported model ID to its download details.
func resolveModelDownload(modelID string) (url, dest, name string) {
	model, ok := setup.FindModel(modelID)
	if !ok {
		return "", "", ""
	}
	return model.DownloadURL, filepath.Join(sussurroModelsDir(), model.Filename), model.Name
}
