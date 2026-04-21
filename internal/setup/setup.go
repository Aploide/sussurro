package setup

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// ProgressCallback is called periodically during model downloads.
// pct is 0–100; downloaded and total are byte counts.
type ProgressCallback func(name string, pct float64, downloaded, total int64)

var (
	progressMu sync.Mutex
	progressCB ProgressCallback
)

// SetProgressCallback installs a callback that receives download progress.
// Pass nil to clear. Safe to call from any goroutine.
func SetProgressCallback(cb ProgressCallback) {
	progressMu.Lock()
	progressCB = cb
	progressMu.Unlock()
}

// DownloadModel downloads a model file from url to destPath with the given
// display name.  Progress is reported via the installed ProgressCallback if any.
func DownloadModel(url, destPath, name string) error {
	return downloadFile(url, destPath, name)
}

// DownloadParakeetV3Bundle downloads all files required to run Parakeet V3 ONNX.
func DownloadParakeetV3Bundle(modelsDir string) error {
	items := []struct {
		url  string
		file string
		name string
	}{
		{url: urlParakeetEncoder, file: fileASRParakeetV3, name: "Parakeet V3 Encoder"},
		{url: urlParakeetDecoder, file: fileParakeetDecoder, name: "Parakeet V3 Decoder"},
		{url: urlParakeetVocab, file: fileParakeetVocab, name: "Parakeet V3 Vocabulary"},
	}
	for _, item := range items {
		if err := downloadFile(item.url, filepath.Join(modelsDir, item.file), item.name); err != nil {
			return err
		}
	}
	return nil
}

// SetActiveModel updates config.yaml to use the given model ID as the active
// ASR model.
func SetActiveModel(modelID string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	modelsDir := filepath.Join(homeDir, ".sussurro", "models")
	configFile := filepath.Join(homeDir, ".sussurro", "config.yaml")

	configBytes, err := os.ReadFile(configFile)
	if err != nil {
		return err
	}

	var newPath, newType, newEngine string
	switch modelID {
	case "whisper-small":
		newPath = filepath.Join(modelsDir, fileASRSmall)
		newType = "whisper"
		newEngine = "native"
	case "whisper-large-v3-turbo":
		newPath = filepath.Join(modelsDir, fileASRLarge)
		newType = "whisper"
		newEngine = "native"
	case "parakeet-v3":
		newPath = filepath.Join(modelsDir, fileASRParakeetV3)
		newType = "parakeet-v3"
		newEngine = "onnx"
	default:
		return fmt.Errorf("unknown model ID: %s", modelID)
	}

	// Replace known ASR paths with the selected one.
	updated := string(configBytes)
	updated = strings.ReplaceAll(updated, filepath.Join(modelsDir, fileASRSmall), newPath)
	updated = strings.ReplaceAll(updated, filepath.Join(modelsDir, fileASRLarge), newPath)
	updated = strings.ReplaceAll(updated, filepath.Join(modelsDir, fileASRParakeetV3), newPath)
	updated = strings.ReplaceAll(updated, filepath.Join(modelsDir, "parakeet-tdt-0.6b-v3.onnx"), newPath) // legacy
	updated = strings.ReplaceAll(updated, `type: "whisper"`, fmt.Sprintf(`type: "%s"`, newType))
	updated = strings.ReplaceAll(updated, `type: "parakeet-v3"`, fmt.Sprintf(`type: "%s"`, newType))
	updated = strings.ReplaceAll(updated, `engine: "native"`, fmt.Sprintf(`engine: "%s"`, newEngine))
	updated = strings.ReplaceAll(updated, `engine: "onnx"`, fmt.Sprintf(`engine: "%s"`, newEngine))

	return os.WriteFile(configFile, []byte(updated), 0644)
}

const (
	defaultConfigTemplate = `app:
  name: "Sussurro"
  debug: false
  log_level: "info" # debug, info, warn, error

audio:
  sample_rate: 16000
  channels: 1
  bit_depth: 16
  buffer_size: 1024
  max_duration: "60s"

models:
  asr:
    path: "{{ASR_PATH}}"
    type: "whisper"
    engine: "native"
    threads: 4
  llm:
    path: "{{LLM_PATH}}"
    context_size: 32768
    gpu_layers: 0
    threads: 4

hotkey:
  trigger: "ctrl+shift+space"
  mode: "push-to-talk" # push-to-talk or toggle

injection:
  method: "keyboard"
`
	// Whisper Small model
	urlASRSmall  = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.bin"
	sizeASRSmall = "488 MB"
	fileASRSmall = "ggml-small.bin"

	// Whisper Large v3 Turbo model
	urlASRLarge  = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3-turbo.bin"
	sizeASRLarge = "1.62 GB"
	fileASRLarge = "ggml-large-v3-turbo.bin"

	// Parakeet V3 ONNX bundle
	urlParakeetEncoder  = "https://huggingface.co/istupakov/parakeet-tdt-0.6b-v3-onnx/resolve/main/encoder-model.int8.onnx"
	urlParakeetDecoder  = "https://huggingface.co/istupakov/parakeet-tdt-0.6b-v3-onnx/resolve/main/decoder_joint-model.int8.onnx"
	urlParakeetVocab    = "https://huggingface.co/istupakov/parakeet-tdt-0.6b-v3-onnx/resolve/main/vocab.txt"
	sizeASRParakeetV3   = "639 MB"
	fileASRParakeetV3   = "encoder-model.int8.onnx"
	fileParakeetDecoder = "decoder_joint-model.int8.onnx"
	fileParakeetVocab   = "vocab.txt"

	// Qwen 3 Sussurro GGUF
	urlLLM  = "https://huggingface.co/cesp99/qwen3-sussurro/resolve/main/qwen3-sussurro-q4_k_m.gguf"
	sizeLLM = "1.28 GB"

	onnxRuntimeVersion      = "1.24.1"
	urlONNXRuntimeLinuxX64  = "https://github.com/microsoft/onnxruntime/releases/download/v1.24.1/onnxruntime-linux-x64-1.24.1.tgz"
	urlONNXRuntimeDarwinARM = "https://github.com/microsoft/onnxruntime/releases/download/v1.24.1/onnxruntime-osx-arm64-1.24.1.tgz"
)

// EnsureSetup checks for the necessary configuration and models,
// and prompts the user to set them up if missing.
func EnsureSetup() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get user home directory: %w", err)
	}

	sussurroDir := filepath.Join(homeDir, ".sussurro")
	modelsDir := filepath.Join(sussurroDir, "models")
	configFile := filepath.Join(sussurroDir, "config.yaml")

	// 1. Create .sussurro directory if it doesn't exist
	if _, err := os.Stat(sussurroDir); os.IsNotExist(err) {
		fmt.Println("Welcome to Sussurro! It looks like this is your first run.")
		fmt.Printf("Creating configuration directory at %s...\n", sussurroDir)
		if err := os.MkdirAll(modelsDir, 0755); err != nil {
			return fmt.Errorf("failed to create directories: %w", err)
		}
	} else {
		// Ensure models dir exists even if sussurro dir exists
		if err := os.MkdirAll(modelsDir, 0755); err != nil {
			return fmt.Errorf("failed to create models directory: %w", err)
		}
	}

	// 2. Create config.yaml if it doesn't exist (defaults to Whisper Small)
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		fmt.Println("Creating default configuration file...")

		defaultASRPath := filepath.Join(modelsDir, fileASRSmall)
		llmDefaultPath := filepath.Join(modelsDir, "qwen3-sussurro-q4_k_m.gguf")

		configContent := strings.ReplaceAll(defaultConfigTemplate, "{{ASR_PATH}}", defaultASRPath)
		configContent = strings.ReplaceAll(configContent, "{{LLM_PATH}}", llmDefaultPath)

		if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
			return fmt.Errorf("failed to write config file: %w", err)
		}
		fmt.Printf("Configuration saved to %s\n", configFile)
	}

	// Determine which ASR model is currently configured
	asrPath := filepath.Join(modelsDir, fileASRSmall) // default
	currentASRIsParakeet := false
	if configBytes, err := os.ReadFile(configFile); err == nil {
		cfgStr := string(configBytes)
		legacyParakeetPath := filepath.Join(modelsDir, "parakeet-tdt-0.6b-v3.onnx")
		if strings.Contains(cfgStr, legacyParakeetPath) {
			cfgStr = strings.ReplaceAll(cfgStr, legacyParakeetPath, filepath.Join(modelsDir, fileASRParakeetV3))
			_ = os.WriteFile(configFile, []byte(cfgStr), 0644)
		}
		oldParakeetEncoderPath := filepath.Join(modelsDir, "encoder-model.onnx")
		if strings.Contains(cfgStr, oldParakeetEncoderPath) {
			cfgStr = strings.ReplaceAll(cfgStr, oldParakeetEncoderPath, filepath.Join(modelsDir, fileASRParakeetV3))
			_ = os.WriteFile(configFile, []byte(cfgStr), 0644)
		}
		if strings.Contains(cfgStr, fileASRLarge) {
			asrPath = filepath.Join(modelsDir, fileASRLarge)
		}
		if strings.Contains(cfgStr, `type: "parakeet-v3"`) || strings.Contains(cfgStr, fileASRParakeetV3) || strings.Contains(cfgStr, "parakeet-tdt-0.6b-v3.onnx") {
			asrPath = filepath.Join(modelsDir, fileASRParakeetV3)
			currentASRIsParakeet = true
		}
	}
	llmPath := filepath.Join(modelsDir, "qwen3-sussurro-q4_k_m.gguf")

	// 3. Check for old model files from versions before v1.3
	entries, err := os.ReadDir(modelsDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			filename := entry.Name()
			// If it's a .gguf file but NOT the new sussurro model, it's an old model
			if strings.HasSuffix(filename, ".gguf") && filename != "qwen3-sussurro-q4_k_m.gguf" {
				oldModelPath := filepath.Join(modelsDir, filename)
				fmt.Println("\n========================================")
				fmt.Println("  OLD MODEL DETECTED - UPDATE REQUIRED")
				fmt.Println("========================================")
				fmt.Printf("Found old model from version < v1.3: %s\n", filename)
				fmt.Println("\nSussurro v1.3+ uses a new fine-tuned model: Qwen 3 Sussurro")
				fmt.Println("The new model provides better transcription cleanup and accuracy.")
				fmt.Printf("\nOld model location: %s\n", oldModelPath)
				fmt.Printf("New model size: %s\n", sizeLLM)
				fmt.Print("\nWould you like to remove the old model and download the new one? (Y/n): ")

				reader := bufio.NewReader(os.Stdin)
				response, _ := reader.ReadString('\n')
				response = strings.TrimSpace(strings.ToLower(response))

				if response == "" || response == "y" || response == "yes" {
					fmt.Printf("Removing old model: %s\n", filename)
					if err := os.Remove(oldModelPath); err != nil {
						fmt.Printf("Warning: Could not remove old model: %v\n", err)
					} else {
						fmt.Println("Old model removed successfully.")
					}

					// Update config file to point to new model
					fmt.Println("Updating configuration file...")
					configContent, err := os.ReadFile(configFile)
					if err == nil {
						// Replace old model path with new one
						oldPathInConfig := filepath.Join(modelsDir, filename)
						newPathInConfig := llmPath
						updatedConfig := strings.ReplaceAll(string(configContent), oldPathInConfig, newPathInConfig)

						if err := os.WriteFile(configFile, []byte(updatedConfig), 0644); err != nil {
							fmt.Printf("Warning: Could not update config file: %v\n", err)
						} else {
							fmt.Println("Configuration updated successfully.")
						}
					}
				}
				break // Only prompt once even if multiple old models exist
			}
		}
	}

	// 4. Check for models and prompt to download
	missingASR := false
	missingLLM := false
	missingONNXRuntime := false

	if currentASRIsParakeet {
		if _, err := os.Stat(filepath.Join(modelsDir, fileASRParakeetV3)); os.IsNotExist(err) {
			missingASR = true
		}
		if _, err := os.Stat(filepath.Join(modelsDir, fileParakeetDecoder)); os.IsNotExist(err) {
			missingASR = true
		}
		if _, err := os.Stat(filepath.Join(modelsDir, fileParakeetVocab)); os.IsNotExist(err) {
			missingASR = true
		}
		if !onnxRuntimeInstalled(homeDir) {
			missingONNXRuntime = true
		}
	} else if _, err := os.Stat(asrPath); os.IsNotExist(err) {
		missingASR = true
	}
	if _, err := os.Stat(llmPath); os.IsNotExist(err) {
		missingLLM = true
	}

	// Ensure ONNX Runtime is available whenever Parakeet is selected,
	// even if all model files are already present.
	if currentASRIsParakeet && missingONNXRuntime {
		if err := installONNXRuntime(homeDir); err != nil {
			return fmt.Errorf("failed to install ONNX Runtime: %w", err)
		}
		missingONNXRuntime = false
	}

	if missingASR || missingLLM {
		// If ASR is missing, ask which Whisper model to use before the download prompt
		chosenASRURL := urlASRSmall
		chosenASRPath := filepath.Join(modelsDir, fileASRSmall)
		chosenASRName := "Whisper Small"
		chosenASRSize := sizeASRSmall

		if missingASR && !currentASRIsParakeet {
			fmt.Println("\nWhich Whisper model would you like to use?")
			fmt.Printf("  [1] Whisper Small         (%s) - faster, lower memory usage\n", sizeASRSmall)
			fmt.Printf("  [2] Whisper Large v3 Turbo (%s) - slower, higher accuracy\n", sizeASRLarge)
			fmt.Print("Enter choice [1/2] (default: 1): ")

			reader := bufio.NewReader(os.Stdin)
			choice, _ := reader.ReadString('\n')
			choice = strings.TrimSpace(choice)

			if choice == "2" {
				chosenASRURL = urlASRLarge
				chosenASRPath = filepath.Join(modelsDir, fileASRLarge)
				chosenASRName = "Whisper Large v3 Turbo"
				chosenASRSize = sizeASRLarge

				// Update config to point to the large model path
				if configBytes, err := os.ReadFile(configFile); err == nil {
					oldSmallPath := filepath.Join(modelsDir, fileASRSmall)
					updated := strings.ReplaceAll(string(configBytes), oldSmallPath, chosenASRPath)
					if err := os.WriteFile(configFile, []byte(updated), 0644); err != nil {
						fmt.Printf("Warning: Could not update config file: %v\n", err)
					}
				}
				asrPath = chosenASRPath
			}
		}

		fmt.Println("\nMissing model files:")
		if missingASR {
			if currentASRIsParakeet {
				chosenASRName = "Parakeet V3 (ONNX)"
				chosenASRPath = filepath.Join(modelsDir, fileASRParakeetV3)
				chosenASRSize = sizeASRParakeetV3
			}
			fmt.Printf(" - %s (ASR): %s (%s)\n", chosenASRName, chosenASRPath, chosenASRSize)
		}
		if missingLLM {
			fmt.Printf(" - LLM Model (Qwen 3 Sussurro): %s (%s)\n", llmPath, sizeLLM)
		}
		if missingONNXRuntime {
			fmt.Printf(" - ONNX Runtime shared library: %s\n", onnxRuntimeLibraryPath(homeDir))
		}

		totalSize := ""
		if missingASR && missingLLM {
			if chosenASRName == "Whisper Large v3 Turbo" {
				totalSize = " (Total: ~2.90 GB)"
			} else {
				totalSize = " (Total: ~1.77 GB)"
			}
		} else if missingASR {
			totalSize = fmt.Sprintf(" (Total: %s)", chosenASRSize)
		} else {
			totalSize = fmt.Sprintf(" (Total: %s)", sizeLLM)
		}

		fmt.Printf("\nWould you like to download them now?%s (Y/n): ", totalSize)
		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))

		if response == "" || response == "y" || response == "yes" {
			if missingASR {
				if currentASRIsParakeet {
					if err := DownloadParakeetV3Bundle(modelsDir); err != nil {
						return fmt.Errorf("failed to download Parakeet V3 model bundle: %w", err)
					}
				} else {
					if err := downloadFile(chosenASRURL, chosenASRPath, chosenASRName); err != nil {
						return fmt.Errorf("failed to download ASR model: %w", err)
					}
				}
			}
			if missingLLM {
				if err := downloadFile(urlLLM, llmPath, "LLM Model"); err != nil {
					return fmt.Errorf("failed to download LLM model: %w", err)
				}
			}
			fmt.Println("\nAll models downloaded successfully!")
		} else {
			fmt.Println("Skipping download. Note: Sussurro may not function correctly without these models.")
		}
	}

	return nil
}

// SwitchWhisperModel lets the user switch between Whisper Small and Whisper Large v3 Turbo.
// It reads the current config, shows the active model, offers the alternative, downloads it
// if needed, and updates the config file.
func SwitchWhisperModel() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get user home directory: %w", err)
	}

	sussurroDir := filepath.Join(homeDir, ".sussurro")
	modelsDir := filepath.Join(sussurroDir, "models")
	configFile := filepath.Join(sussurroDir, "config.yaml")

	smallPath := filepath.Join(modelsDir, fileASRSmall)
	largePath := filepath.Join(modelsDir, fileASRLarge)

	// Read config to determine the currently configured model
	configBytes, err := os.ReadFile(configFile)
	if err != nil {
		return fmt.Errorf("could not read config file at %s: %w\nRun 'sussurro' first to complete initial setup", configFile, err)
	}
	configStr := string(configBytes)

	currentIsLarge := strings.Contains(configStr, fileASRLarge)
	var currentName, currentSize string
	if currentIsLarge {
		currentName = "Whisper Large v3 Turbo"
		currentSize = sizeASRLarge
	} else {
		currentName = "Whisper Small"
		currentSize = sizeASRSmall
	}

	fmt.Printf("\nCurrent Whisper model: %s (%s)\n", currentName, currentSize)
	fmt.Println("\nAvailable models:")
	fmt.Printf("  [1] Whisper Small         (%s) - faster, lower memory usage\n", sizeASRSmall)
	fmt.Printf("  [2] Whisper Large v3 Turbo (%s) - slower, higher accuracy\n", sizeASRLarge)
	fmt.Print("\nEnter choice [1/2]: ")

	reader := bufio.NewReader(os.Stdin)
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)

	var targetPath, targetURL, targetName, targetSize string
	switch choice {
	case "1":
		targetPath = smallPath
		targetURL = urlASRSmall
		targetName = "Whisper Small"
		targetSize = sizeASRSmall
	case "2":
		targetPath = largePath
		targetURL = urlASRLarge
		targetName = "Whisper Large v3 Turbo"
		targetSize = sizeASRLarge
	default:
		fmt.Println("Invalid choice. No changes made.")
		return nil
	}

	// Check if already using this model
	if (choice == "1" && !currentIsLarge) || (choice == "2" && currentIsLarge) {
		fmt.Printf("Already using %s. No changes needed.\n", targetName)
		return nil
	}

	// Download the target model if not already present
	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		fmt.Printf("\n%s not found locally (%s). Download now? (Y/n): ", targetName, targetSize)
		resp, _ := reader.ReadString('\n')
		resp = strings.TrimSpace(strings.ToLower(resp))
		if resp != "" && resp != "y" && resp != "yes" {
			fmt.Println("Download cancelled. No changes made.")
			return nil
		}
		if err := downloadFile(targetURL, targetPath, targetName); err != nil {
			return fmt.Errorf("failed to download %s: %w", targetName, err)
		}
		fmt.Println()
	}

	// Update config: replace the current ASR path with the new one
	var oldPath string
	if currentIsLarge {
		oldPath = largePath
	} else {
		oldPath = smallPath
	}
	updatedConfig := strings.ReplaceAll(configStr, oldPath, targetPath)

	if err := os.WriteFile(configFile, []byte(updatedConfig), 0644); err != nil {
		return fmt.Errorf("failed to update config file: %w", err)
	}

	fmt.Printf("\nSwitched to %s successfully!\n", targetName)
	fmt.Printf("Config updated: %s\n", configFile)
	return nil
}

// downloadFile downloads a file from url to filepath with a simple progress indicator
func downloadFile(url, filepath, name string) error {
	fmt.Printf("Downloading %s...\n", name)

	// Download into a temp file first, then atomically move into place.
	// This avoids leaving zero-byte / partial files when a download fails.
	tmpPath := filepath + ".part"
	out, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer func() {
		out.Close()
		_ = os.Remove(tmpPath)
	}()

	// Get the data
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	// Create a proxy reader to track progress
	contentLength := resp.ContentLength
	reader := &progressReader{
		Reader: resp.Body,
		Total:  contentLength,
		Name:   name,
	}

	_, err = io.Copy(out, reader)
	fmt.Println() // Newline after progress
	if err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, filepath)
}

func onnxRuntimeLibraryPath(homeDir string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(homeDir, ".sussurro", "onnxruntime", "libonnxruntime.dylib")
	}
	return filepath.Join(homeDir, ".sussurro", "onnxruntime", "libonnxruntime.so."+onnxRuntimeVersion)
}

func onnxRuntimeInstalled(homeDir string) bool {
	_, err := os.Stat(onnxRuntimeLibraryPath(homeDir))
	return err == nil
}

func installONNXRuntime(homeDir string) error {
	fmt.Println("Installing ONNX Runtime...")
	targetLib := onnxRuntimeLibraryPath(homeDir)
	targetDir := filepath.Dir(targetLib)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}

	var archiveURL string
	var archiveRoot string
	switch runtime.GOOS {
	case "linux":
		if runtime.GOARCH != "amd64" {
			return fmt.Errorf("automatic ONNX Runtime install is supported only on linux/amd64 and darwin/arm64; set SUSSURRO_ONNXRUNTIME_LIB manually")
		}
		archiveURL = urlONNXRuntimeLinuxX64
		archiveRoot = "onnxruntime-linux-x64-" + onnxRuntimeVersion
	case "darwin":
		if runtime.GOARCH != "arm64" {
			return fmt.Errorf("automatic ONNX Runtime install is supported only on linux/amd64 and darwin/arm64; set SUSSURRO_ONNXRUNTIME_LIB manually")
		}
		archiveURL = urlONNXRuntimeDarwinARM
		archiveRoot = "onnxruntime-osx-arm64-" + onnxRuntimeVersion
	default:
		return fmt.Errorf("automatic ONNX Runtime install is not supported on %s; set SUSSURRO_ONNXRUNTIME_LIB manually", runtime.GOOS)
	}

	tmpDir, err := os.MkdirTemp("", "sussurro-onnxruntime-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, "onnxruntime.tgz")
	if err := downloadFile(archiveURL, archivePath, "ONNX Runtime"); err != nil {
		return err
	}

	cmd := exec.Command("tar", "-xzf", archivePath, "-C", tmpDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to extract ONNX Runtime archive: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	var srcLib string
	if runtime.GOOS == "darwin" {
		srcLib = filepath.Join(tmpDir, archiveRoot, "lib", "libonnxruntime.dylib")
	} else {
		srcLib = filepath.Join(tmpDir, archiveRoot, "lib", "libonnxruntime.so."+onnxRuntimeVersion)
	}
	if _, err := os.Stat(srcLib); err != nil {
		return fmt.Errorf("could not find extracted ONNX Runtime library at %s", srcLib)
	}

	in, err := os.Open(srcLib)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(targetLib)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}

	if runtime.GOOS == "linux" {
		_ = os.Remove(filepath.Join(targetDir, "libonnxruntime.so"))
		_ = os.Symlink(filepath.Base(targetLib), filepath.Join(targetDir, "libonnxruntime.so"))
	}

	fmt.Printf("ONNX Runtime installed at %s\n", targetLib)
	return nil
}

func (pr *progressReader) invokeCallback() {
	progressMu.Lock()
	cb := progressCB
	progressMu.Unlock()
	if cb != nil {
		pct := 0.0
		if pr.Total > 0 {
			pct = float64(pr.Current) / float64(pr.Total) * 100
		}
		cb(pr.Name, pct, pr.Current, pr.Total)
	}
}

type progressReader struct {
	io.Reader
	Total   int64
	Current int64
	Name    string
	Last    int64
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.Reader.Read(p)
	pr.Current += int64(n)

	// Update progress every 1MB or so to avoid spamming stdout
	if pr.Current-pr.Last > 1024*1024 || pr.Current == pr.Total {
		pr.Last = pr.Current
		if pr.Total > 0 {
			percent := float64(pr.Current) / float64(pr.Total) * 100
			fmt.Printf("\rDownloading %s: %.1f%% (%.1f/%.1f MB)", pr.Name, percent, float64(pr.Current)/1024/1024, float64(pr.Total)/1024/1024)
		} else {
			fmt.Printf("\rDownloading %s: %.1f MB", pr.Name, float64(pr.Current)/1024/1024)
		}
		pr.invokeCallback()
	}

	return n, err
}
