package pipeline

import "testing"

func TestResolveMaxDuration(t *testing.T) {
	tests := []struct {
		name, configured, want string
		wantErr                bool
	}{
		{name: "missing", want: "2m"},
		{name: "configured", configured: "45s", want: "45s"},
		{name: "invalid", configured: "later", want: "2m", wantErr: true},
		{name: "negative", configured: "-1s", want: "2m", wantErr: true},
		{name: "zero means unlimited", configured: "0", want: "infinite"},
		{name: "unlimited", configured: "INFINITE", want: "infinite"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveMaxDuration(tt.configured)
			if got != tt.want {
				t.Errorf("ResolveMaxDuration(%q) = %q, want %q", tt.configured, got, tt.want)
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("ResolveMaxDuration(%q) error = %v, wantErr %v", tt.configured, err, tt.wantErr)
			}
		})
	}
}

func TestAudioBufferCapUsesResolvedLimit(t *testing.T) {
	const sampleRate = 16000
	const twoMinutes = 120 * sampleRate
	for _, configured := range []string{"", "2m", "invalid"} {
		if got := audioBufferCapFor(configured, sampleRate); got != twoMinutes {
			t.Errorf("audioBufferCapFor(%q) = %d, want %d", configured, got, twoMinutes)
		}
	}
}
