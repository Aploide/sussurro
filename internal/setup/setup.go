package setup

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/aploide/sussurro/internal/config"
	"go.yaml.in/yaml/v3"
)

// ProgressCallback is called periodically during model downloads.
// pct is 0–100; downloaded and total are byte counts.
type ProgressCallback func(name string, pct float64, downloaded, total int64)

// DownloadModel downloads a model file and reports progress only to this
// download's callback, so concurrent downloads cannot cross-wire their UI.
func DownloadModel(url, destPath, name string, progress ProgressCallback) error {
	if err := downloadFileWithProgress(url, destPath, name, progress); err != nil {
		return fmt.Errorf("download %s: %w", name, err)
	}
	return nil
}

// EnsureVADModel downloads the setup-managed voice-activity model when an
// existing configuration predates it or contains an incomplete download.
func EnsureVADModel(destPath string, outputs ...io.Writer) error {
	output := io.Writer(os.Stdout)
	if len(outputs) > 0 && outputs[0] != nil {
		output = outputs[0]
	}

	info, err := os.Stat(destPath)
	if err == nil && info.Size() >= config.MinimumVADModelSize {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect VAD model: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("failed to create VAD model directory: %w", err)
	}
	if err := downloadFileToWriter(config.DefaultVADModelURL, destPath, "Silero voice activity model", output); err != nil {
		return fmt.Errorf("failed to download VAD model: %w", err)
	}
	return nil
}

// ActivateModel persists and activates an installed model from the supported catalog.
func ActivateModel(cfg *config.Config, modelID string) error {
	model, ok := FindModel(modelID)
	if !ok {
		return fmt.Errorf("unknown model ID: %s", modelID)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	newPath := filepath.Join(homeDir, ".sussurro", "models", model.Filename)
	info, err := os.Stat(newPath)
	if err != nil {
		return fmt.Errorf("model is not installed: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("model is not a regular file: %s", newPath)
	}

	role := config.ModelRoleASR
	if model.Kind == ModelKindLLM {
		role = config.ModelRoleLLM
	}
	if err := config.SaveModelPath(cfg, role, newPath); err != nil {
		return err
	}
	if model.Kind == ModelKindLLM {
		cfg.Models.LLM.Path = newPath
	} else {
		cfg.Models.ASR.Path = newPath
	}
	return nil
}

const (
	defaultConfigTemplate = `app:
  name: "Sussurro"
  debug: false
  log_level: "info" # debug, info, warn, error
  # Personal dictionary: names and terms the cleanup stage must spell exactly
  # as written here (fixes words the ASR tends to mishear).
  dictionary: []

audio:
  sample_rate: 16000
  channels: 1
  bit_depth: 16
  buffer_size: 1024
  max_duration: "2m"

models:
  asr:
    path: {{ASR_PATH}}
    vad_path: {{VAD_PATH}}
    vad_threshold: 0.01
    type: "whisper"
    threads: 4
  llm:
    path: {{LLM_PATH}}
    context_size: 4096
    gpu_layers: 99
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

	// Silero voice-activity model used by whisper.cpp
	sizeVAD = "885 KB"
	fileVAD = config.DefaultVADModelFilename
)

func configuredLLMPath(configFile, fallback string) string {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return fallback
	}
	var paths struct {
		Models struct {
			LLM struct {
				Path string `yaml:"path"`
			} `yaml:"llm"`
		} `yaml:"models"`
	}
	if yaml.Unmarshal(data, &paths) != nil || paths.Models.LLM.Path == "" {
		return fallback
	}
	return paths.Models.LLM.Path
}

func configuredVADPath(configFile, fallback string) string {
	if path := os.Getenv("SUSSURRO_MODELS_ASR_VAD_PATH"); path != "" {
		return path
	}
	data, err := os.ReadFile(configFile)
	if err != nil {
		return fallback
	}
	var paths struct {
		Models struct {
			ASR struct {
				VADPath string `yaml:"vad_path"`
			} `yaml:"asr"`
		} `yaml:"models"`
	}
	if yaml.Unmarshal(data, &paths) != nil || paths.Models.ASR.VADPath == "" {
		return fallback
	}
	return paths.Models.ASR.VADPath
}

// EnsureSetup checks for the necessary configuration and models,
// and prompts the user to set them up if missing. When configPath is given,
// model paths introduced by migrations are read from that configuration.
func EnsureSetup(configPaths ...string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get user home directory: %w", err)
	}

	sussurroDir := filepath.Join(homeDir, ".sussurro")
	modelsDir := filepath.Join(sussurroDir, "models")
	configFile := filepath.Join(sussurroDir, "config.yaml")
	if len(configPaths) > 0 && configPaths[0] != "" {
		configFile = configPaths[0]
	}

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
		vadDefaultPath := filepath.Join(modelsDir, fileVAD)
		llmDefaultPath := filepath.Join(modelsDir, "qwen3-sussurro-q4_k_m.gguf")

		// The placeholders stand where a quoted scalar goes, so the paths are
		// substituted already quoted (a bare Windows path breaks YAML).
		configContent := strings.ReplaceAll(defaultConfigTemplate, "{{ASR_PATH}}", config.YAMLPathLiteral(defaultASRPath))
		configContent = strings.ReplaceAll(configContent, "{{VAD_PATH}}", config.YAMLPathLiteral(vadDefaultPath))
		configContent = strings.ReplaceAll(configContent, "{{LLM_PATH}}", config.YAMLPathLiteral(llmDefaultPath))

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
	vadPath := configuredVADPath(configFile, filepath.Join(modelsDir, fileVAD))
	llmPath := configuredLLMPath(configFile, filepath.Join(modelsDir, "qwen3-sussurro-q4_k_m.gguf"))
	llmModel, managedLLM := FindModelByFilename(filepath.Base(llmPath))
	if !managedLLM {
		llmModel, _ = FindModel("qwen3-sussurro-q4-k-m")
	}

	// 3. Check for old model files from versions before v1.3
	entries, err := os.ReadDir(modelsDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			filename := entry.Name()
			if strings.HasSuffix(filename, ".gguf") {
				oldModelPath := filepath.Join(modelsDir, filename)
				if _, supported := FindModelByFilename(filename); supported || filepath.Clean(oldModelPath) == filepath.Clean(llmPath) {
					continue
				}
				fmt.Println("\n========================================")
				fmt.Println("  OLD MODEL DETECTED - UPDATE REQUIRED")
				fmt.Println("========================================")
				fmt.Printf("Found old model from version < v1.3: %s\n", filename)
				fmt.Println("\nSussurro v1.3+ uses a new fine-tuned model: Qwen 3 Sussurro")
				fmt.Println("The new model provides better transcription cleanup and accuracy.")
				fmt.Printf("\nOld model location: %s\n", oldModelPath)
				fmt.Printf("New model size: %s\n", defaultLLMSize)
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
				}
				break // Only prompt once even if multiple old models exist
			}
		}
	}

	// 4. Check for models and prompt to download
	missingASR := false
	missingVAD := false
	missingLLM := false

	if _, err := os.Stat(asrPath); os.IsNotExist(err) {
		missingASR = true
	}
	if info, err := os.Stat(vadPath); os.IsNotExist(err) || (err == nil && info.Size() < config.MinimumVADModelSize) {
		missingVAD = true
	}
	if _, err := os.Stat(llmPath); os.IsNotExist(err) {
		if !managedLLM {
			return fmt.Errorf("configured LLM model is missing and cannot be downloaded automatically: %s", llmPath)
		}
		missingLLM = true
	}

	if missingASR || missingVAD || missingLLM {
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
					updated := config.ReplacePathInYAML(string(configBytes), oldSmallPath, chosenASRPath)
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
		if missingVAD {
			fmt.Printf(" - Silero voice activity model: %s (%s)\n", vadPath, sizeVAD)
		}
		if missingLLM {
			fmt.Printf(" - %s (LLM): %s (%s)\n", llmModel.Name, llmPath, llmModel.Size)
		}

		totalSize := ""
		if missingASR && missingLLM {
			totalSize = fmt.Sprintf(" (%s + %s)", chosenASRSize, llmModel.Size)
		} else if missingASR {
			totalSize = fmt.Sprintf(" (Total: %s)", chosenASRSize)
		} else if missingLLM {
			totalSize = fmt.Sprintf(" (Total: %s)", llmModel.Size)
		} else {
			totalSize = fmt.Sprintf(" (Total: %s)", sizeVAD)
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
			if missingVAD {
				if err := EnsureVADModel(vadPath); err != nil {
					return fmt.Errorf("provision VAD model: %w", err)
				}
			}
			if missingLLM {
				if err := downloadFile(llmModel.DownloadURL, llmPath, llmModel.Name); err != nil {
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

// downloadFile downloads a file from url to filepath with a simple progress
// indicator. The final path appears only after a complete download, so a
// network failure cannot leave a partial model that future setup runs trust.
func downloadFile(url, destPath, name string) error {
	return downloadFileWithProgress(url, destPath, name, nil)
}

func downloadFileWithProgress(url, destPath, name string, progress ProgressCallback) error {
	if err := downloadFileToWriterWithProgress(url, destPath, name, os.Stdout, progress); err != nil {
		return fmt.Errorf("download %s: %w", name, err)
	}
	return nil
}

func downloadFileToWriter(url, destPath, name string, output io.Writer) error {
	return downloadFileToWriterWithProgress(url, destPath, name, output, nil)
}

func downloadFileToWriterWithProgress(url, destPath, name string, output io.Writer, progress ProgressCallback) error {
	fmt.Fprintf(output, "Downloading %s...\n", name)

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("request model: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	out, err := os.CreateTemp(filepath.Dir(destPath), "."+filepath.Base(destPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary model file: %w", err)
	}
	tmpPath := out.Name()
	defer os.Remove(tmpPath)

	reader := &progressReader{
		Reader:   resp.Body,
		Total:    resp.ContentLength,
		Name:     name,
		Output:   output,
		Callback: progress,
	}
	if _, err := io.Copy(out, reader); err != nil {
		if closeErr := out.Close(); closeErr != nil {
			return fmt.Errorf("copy model data: %w (also failed to close temporary file: %v)", err, closeErr)
		}
		return fmt.Errorf("copy model data: %w", err)
	}
	fmt.Fprintln(output) // Newline after progress
	if err := out.Close(); err != nil {
		return fmt.Errorf("close temporary model file: %w", err)
	}
	if renameErr := os.Rename(tmpPath, destPath); renameErr != nil {
		// Windows does not replace an existing destination. This path is used
		// when setup is replacing an incomplete model, after the full new file
		// has already reached the sibling temporary file.
		if _, statErr := os.Stat(destPath); statErr != nil {
			return fmt.Errorf("install model: %w", renameErr)
		}
		if removeErr := os.Remove(destPath); removeErr != nil {
			return fmt.Errorf("replace incomplete model: %w", removeErr)
		}
		if err := os.Rename(tmpPath, destPath); err != nil {
			return fmt.Errorf("install replacement model: %w", err)
		}
	}
	return nil
}

func (pr *progressReader) invokeCallback() {
	if pr.Callback == nil {
		return
	}
	pct := 0.0
	if pr.Total > 0 {
		pct = float64(pr.Current) / float64(pr.Total) * 100
	}
	pr.Callback(pr.Name, pct, pr.Current, pr.Total)
}

type progressReader struct {
	io.Reader
	Total    int64
	Current  int64
	Name     string
	Last     int64
	Output   io.Writer
	Callback ProgressCallback
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.Reader.Read(p)
	pr.Current += int64(n)

	// Update progress every 1MB or so to avoid spamming stdout
	if pr.Current-pr.Last > 1024*1024 || pr.Current == pr.Total {
		pr.Last = pr.Current
		if pr.Total > 0 {
			percent := float64(pr.Current) / float64(pr.Total) * 100
			fmt.Fprintf(pr.Output, "\rDownloading %s: %.1f%% (%.1f/%.1f MB)", pr.Name, percent, float64(pr.Current)/1024/1024, float64(pr.Total)/1024/1024)
		} else {
			fmt.Fprintf(pr.Output, "\rDownloading %s: %.1f MB", pr.Name, float64(pr.Current)/1024/1024)
		}
		pr.invokeCallback()
	}

	return n, err
}
