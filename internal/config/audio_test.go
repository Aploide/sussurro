package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestLoadConfigDefaultsLiveCaptureFormat(t *testing.T) {
	isolateAudioEnv(t)
	cfg := loadAudioConfig(t, "{}\n")
	if got := cfg.Audio.SampleRate; got != WhisperSampleRate {
		t.Errorf("audio.sample_rate = %d, want %d", got, WhisperSampleRate)
	}
	if got := cfg.Audio.Channels; got != WhisperChannels {
		t.Errorf("audio.channels = %d, want %d", got, WhisperChannels)
	}
	if err := cfg.Audio.ValidateLiveCapture(); err != nil {
		t.Errorf("ValidateLiveCapture() error = %v", err)
	}
}

func TestValidateLiveCaptureRejectsUnsupportedFormat(t *testing.T) {
	tests := []struct {
		name, yaml, field string
	}{
		{name: "48 kHz", yaml: "audio:\n  sample_rate: 48000\n", field: "audio.sample_rate"},
		{name: "zero sample rate", yaml: "audio:\n  sample_rate: 0\n", field: "audio.sample_rate"},
		{name: "stereo", yaml: "audio:\n  channels: 2\n", field: "audio.channels"},
		{name: "zero channels", yaml: "audio:\n  channels: 0\n", field: "audio.channels"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateAudioEnv(t)
			cfg := loadAudioConfig(t, tt.yaml)
			err := cfg.Audio.ValidateLiveCapture()
			if err == nil {
				t.Fatalf("ValidateLiveCapture() accepted %s", tt.name)
			}
			if !strings.Contains(err.Error(), tt.field) {
				t.Errorf("ValidateLiveCapture() error = %q, want %s", err, tt.field)
			}
		})
	}
}

func TestLiveCaptureFormatEnvironmentOverride(t *testing.T) {
	for _, sampleRate := range []string{"16000", "48000"} {
		t.Run(sampleRate, func(t *testing.T) {
			isolateAudioEnv(t)
			viper.Reset()
			t.Cleanup(viper.Reset)
			t.Setenv("SUSSURRO_AUDIO_SAMPLE_RATE", sampleRate)
			cfg := loadAudioConfigFile(t, "{}\n")
			err := cfg.Audio.ValidateLiveCapture()
			if sampleRate == "16000" && err != nil {
				t.Errorf("ValidateLiveCapture() error = %v", err)
			}
			if sampleRate == "48000" {
				if err == nil {
					t.Error("ValidateLiveCapture() accepted environment override of 48000")
				} else if !strings.Contains(err.Error(), "audio.sample_rate") {
					t.Errorf("ValidateLiveCapture() error = %q, want audio.sample_rate", err)
				}
			}
		})
	}
}

func loadAudioConfig(t *testing.T, body string) *Config {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	return loadAudioConfigFile(t, body)
}

func loadAudioConfigFile(t *testing.T, body string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func isolateAudioEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"SUSSURRO_AUDIO_SAMPLE_RATE", "SUSSURRO_AUDIO_CHANNELS"} {
		value, existed := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if existed {
				_ = os.Setenv(key, value)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
}
