package pipeline

import "testing"

func TestStripNonSpeechMarkers(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			// The real output captured while verifying sussurro-xvj.36.
			name: "observed live output",
			in:   "[MUSIC] You need me to be speaking while I'm holding it, I guess. [MUSIC] All right, that should be 30 seconds by now. [MUSIC]",
			want: "You need me to be speaking while I'm holding it, I guess. All right, that should be 30 seconds by now.",
		},
		{"blank audio", "[BLANK_AUDIO]", ""},
		{"blank audio spaced", "[BLANK AUDIO]", ""},
		{"only markers", "[MUSIC] [SOUND]", ""},
		{"parenthesised", "(laughter) That was funny.", "That was funny."},
		{"asterisked", "*coughs* Excuse me.", "Excuse me."},
		{"case insensitive", "[Music] Hello.", "Hello."},
		{"mid sentence", "The thing [NOISE] works.", "The thing works."},
		{"before punctuation", "I was talking [MUSIC], then stopped.", "I was talking, then stopped."},
		{"empty", "", ""},
		{"no markers untouched", "Just ordinary dictated text.", "Just ordinary dictated text."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := StripNonSpeechMarkers(tc.in); got != tc.want {
				t.Errorf("StripNonSpeechMarkers(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// The filter must not eat real speech. A dictated parenthetical is a clause,
// which is what the length bound distinguishes from a marker.
func TestStripNonSpeechMarkersKeepsRealSpeech(t *testing.T) {
	keep := []string{
		"I went to the shop (the one on the corner, past the church) and bought milk.",
		"Use the flag (it takes a value longer than a marker would be) when running it.",
		"Multiply (a plus b) by the total and see what happens next.",
	}

	for _, in := range keep {
		if got := StripNonSpeechMarkers(in); got != in {
			t.Errorf("stripped real speech\n  in: %q\n got: %q", in, got)
		}
	}
}
