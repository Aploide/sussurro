package config

import "fmt"

const (
	WhisperSampleRate = 16000
	WhisperChannels   = 1
)

// ValidateLiveCapture rejects formats the live path would pass to Whisper
// without resampling or downmixing.
func (a AudioConfig) ValidateLiveCapture() error {
	if a.SampleRate != WhisperSampleRate {
		return fmt.Errorf("audio.sample_rate must be %d because live capture is not resampled for Whisper", WhisperSampleRate)
	}
	if a.Channels != WhisperChannels {
		return fmt.Errorf("audio.channels must be %d because live capture is not downmixed for Whisper", WhisperChannels)
	}
	return nil
}
