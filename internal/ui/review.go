package ui

import (
	"sync"

	"github.com/aploide/sussurro/internal/session"
)

// ReviewPresenter adapts the review controller's presentation callbacks to the
// UI Manager. It holds the latest transcript so a state change alone can
// re-render the card without the controller resending text.
type ReviewPresenter struct {
	present func(model ViewModel)

	mu         sync.Mutex
	state      session.ReviewState
	transcript string
	partial    bool
}

// NewReviewPresenter builds a presenter that publishes to present.
func NewReviewPresenter(present func(model ViewModel)) *ReviewPresenter {
	return &ReviewPresenter{present: present}
}

// OnReviewState implements session.Presenter.
func (p *ReviewPresenter) OnReviewState(state session.ReviewState) {
	p.mu.Lock()
	p.state = state
	// Leaving Ready-and-beyond clears stale text so a new dictation never
	// shows the previous one while it is being recorded.
	if state == session.ReviewIdle || state == session.ReviewRecording {
		p.transcript = ""
		p.partial = false
	}
	model := p.model()
	p.mu.Unlock()

	p.publish(model)
}

// OnPartialText implements session.Presenter.
func (p *ReviewPresenter) OnPartialText(text string) {
	p.mu.Lock()
	p.transcript = text
	p.partial = true
	model := p.model()
	p.mu.Unlock()

	p.publish(model)
}

// OnReviewText implements session.Presenter.
func (p *ReviewPresenter) OnReviewText(text string) {
	p.mu.Lock()
	p.transcript = text
	p.partial = false
	model := p.model()
	p.mu.Unlock()

	p.publish(model)
}

// OnDeliveryError implements session.Presenter. The transcript stays on screen
// so a failed delivery never looks like lost dictation.
func (p *ReviewPresenter) OnDeliveryError(err error) {
	p.mu.Lock()
	model := ErrorModel(p.state, p.transcript, "Delivery failed: "+err.Error())
	p.mu.Unlock()

	p.publish(model)
}

// model builds the current view. Caller holds p.mu.
func (p *ReviewPresenter) model() ViewModel {
	return ReviewModel(p.state, p.transcript, p.partial)
}

// publish forwards a model, tolerating an unset sink for headless use.
func (p *ReviewPresenter) publish(model ViewModel) {
	if p.present != nil {
		p.present(model)
	}
}
