package setup

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentDownloadsKeepProgressCallbacksSeparate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat(r.URL.Path, 1024*1024))
	}))
	defer server.Close()

	type result struct {
		wantName string
		names    []string
		err      error
	}
	results := []*result{{wantName: "Q5"}, {wantName: "Q8"}}
	var wg sync.WaitGroup
	for i, item := range results {
		wg.Add(1)
		go func(index int, current *result) {
			defer wg.Done()
			current.err = downloadFileToWriterWithProgress(
				server.URL+"/"+current.wantName,
				filepath.Join(t.TempDir(), "model.gguf"),
				current.wantName,
				io.Discard,
				func(name string, _ float64, _, _ int64) { current.names = append(current.names, name) },
			)
		}(i, item)
	}
	wg.Wait()

	for _, item := range results {
		if item.err != nil {
			t.Errorf("%s download error = %v", item.wantName, item.err)
		}
		if len(item.names) == 0 {
			t.Errorf("%s received no progress callback", item.wantName)
		}
		for _, name := range item.names {
			if name != item.wantName {
				t.Errorf("%s callback received progress for %q", item.wantName, name)
			}
		}
	}
}
