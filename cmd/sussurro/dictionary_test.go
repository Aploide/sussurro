package main

import (
	"reflect"
	"testing"
)

type recordingDictionaryConsumer struct {
	calls [][]string
}

func (c *recordingDictionaryConsumer) SetDictionary(terms []string) {
	c.calls = append(c.calls, append([]string(nil), terms...))
}

func TestDictionaryFanoutUpdatesAndClearsEveryConsumer(t *testing.T) {
	asr := &recordingDictionaryConsumer{}
	cleanup := &recordingDictionaryConsumer{}
	fanout := dictionaryFanout{asr, cleanup}

	terms := []string{"Sussurro", "whisper.cpp"}
	fanout.SetDictionary(terms)
	terms[0] = "mutated by caller"
	fanout.SetDictionary(nil)

	want := [][]string{{"Sussurro", "whisper.cpp"}, nil}
	for name, consumer := range map[string]*recordingDictionaryConsumer{
		"decoder": asr,
		"cleanup": cleanup,
	} {
		if !reflect.DeepEqual(consumer.calls, want) {
			t.Errorf("%s calls = %#v, want %#v", name, consumer.calls, want)
		}
	}
}
