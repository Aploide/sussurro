package setup

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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

// SetActiveModel updates config.yaml to use the given model ID as the active
// ASR model.  modelID is one of: "whisper-small", "whisper-large-v3-turbo".
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

	var newPath string
	switch modelID {
	case "whisper-small":
		newPath = filepath.Join(modelsDir, fileASRSmall)
	case "whisper-large-v3-turbo":
		newPath = filepath.Join(modelsDir, fileASRLarge)
	default:
		return fmt.Errorf("unknown model ID: %s", modelID)
	}

	// Replace either known ASR path with the new one
	updated := string(configBytes)
	updated = strings.ReplaceAll(updated, filepath.Join(modelsDir, fileASRSmall), newPath)
	updated = strings.ReplaceAll(updated, filepath.Join(modelsDir, fileASRLarge), newPath)

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
    threads: 4
  llm:
    path: "{{LLM_PATH}}"
    context_size: 32768
    gpu_layers: 0
    threads: 4

hotkey:
  trigger: "ctrl+shift+space"
  toggle_trigger: "ctrl+shift+enter"

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

	// Qwen 3 Sussurro GGUF
	urlLLM  = "https://huggingface.co/cesp99/qwen3-sussurro/resolve/main/qwen3-sussurro-q4_k_m.gguf"
	sizeLLM = "1.28 GB"
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
	if configBytes, err := os.ReadFile(configFile); err == nil {
		if strings.Contains(string(configBytes), fileASRLarge) {
			asrPath = filepath.Join(modelsDir, fileASRLarge)
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

	if _, err := os.Stat(asrPath); os.IsNotExist(err) {
		missingASR = true
	}
	if _, err := os.Stat(llmPath); os.IsNotExist(err) {
		missingLLM = true
	}

	if missingASR || missingLLM {
		// If ASR is missing, ask which Whisper model to use before the download prompt
		chosenASRURL := urlASRSmall
		chosenASRPath := filepath.Join(modelsDir, fileASRSmall)
		chosenASRName := "Whisper Small"
		chosenASRSize := sizeASRSmall

		if missingASR {
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
			fmt.Printf(" - %s (ASR): %s (%s)\n", chosenASRName, chosenASRPath, chosenASRSize)
		}
		if missingLLM {
			fmt.Printf(" - LLM Model (Qwen 3 Sussurro): %s (%s)\n", llmPath, sizeLLM)
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
				if err := downloadFile(chosenASRURL, chosenASRPath, chosenASRName); err != nil {
					return fmt.Errorf("failed to download ASR model: %w", err)
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

	// Create the file
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

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
	return err
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
