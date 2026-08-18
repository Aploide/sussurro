package main

import (
	"sync"

	"github.com/aploide/sussurro/internal/ui"
)

// presentSink forwards review presentation to whatever sink is installed. The
// workflow is wired before the UI manager exists, so the sink is swapped in
// once the manager is running rather than threading construction order through
// the whole setup.
type presentSink struct {
	mu      sync.RWMutex
	present func(model ui.ViewModel)
}

// Present forwards a view model to the current sink.
func (s *presentSink) Present(model ui.ViewModel) {
	s.mu.RLock()
	present := s.present
	s.mu.RUnlock()
	if present != nil {
		present(model)
	}
}

// Set replaces the sink. Safe to call while the workflow is running.
func (s *presentSink) Set(present func(model ui.ViewModel)) {
	s.mu.Lock()
	s.present = present
	s.mu.Unlock()
}
