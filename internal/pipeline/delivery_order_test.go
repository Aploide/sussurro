package pipeline

import (
	"testing"
	"time"

	ctxProvider "github.com/aploide/sussurro/internal/context"
	"github.com/aploide/sussurro/internal/session"
)

// orderRecorder timestamps delivery and the completion notification so their
// ordering can be observed rather than inferred from reading the code.
type orderRecorder struct {
	delivered time.Time
	finished  time.Time
}

func (r *orderRecorder) OnResult(Result)                             { r.delivered = time.Now() }
func (r *orderRecorder) OnStateChange(session.State)                 {}
func (r *orderRecorder) OnRMSData(float32)                           {}
func (r *orderRecorder) OnPhase(state session.State, partial string) {}
func (r *orderRecorder) OnFinished(text string)                      { r.finished = time.Now() }

// TestDeliveryPrecedesTheCompletionNotification covers sussurro-xvj.45: the
// overlay's one second linger is presentational and must not sit on the
// delivery path. Text has to reach the clipboard as soon as it is ready.
func TestDeliveryPrecedesTheCompletionNotification(t *testing.T) {
	recorder := &orderRecorder{}

	p := newTestPipeline(t,
		&stubTranscriber{text: "the quick brown fox"},
		&stubCleaner{text: "The quick brown fox."},
		stubContext{info: &ctxProvider.ContextInfo{}})
	p.SetResultConsumer(recorder)
	p.uiNotifier = recorder

	run(p, samplesFor(3))

	if recorder.delivered.IsZero() {
		t.Fatal("result was never delivered")
	}
	if recorder.finished.IsZero() {
		t.Fatal("completion was never notified")
	}
	if !recorder.delivered.Before(recorder.finished) {
		t.Errorf("delivery at %v did not precede the completion notification at %v; "+
			"the overlay's hold time would delay the clipboard",
			recorder.delivered, recorder.finished)
	}
}

// TestDeliveryDoesNotWaitOnTheOverlay checks the gap between the two is
// negligible. A notifier that blocks, as a linger implemented inline would,
// must not push delivery later.
func TestDeliveryDoesNotWaitOnTheOverlay(t *testing.T) {
	const overlayWork = 200 * time.Millisecond

	recorder := &orderRecorder{}
	slow := &slowNotifier{orderRecorder: recorder, delay: overlayWork}

	p := newTestPipeline(t,
		&stubTranscriber{text: "the quick brown fox"},
		&stubCleaner{text: "The quick brown fox."},
		stubContext{info: &ctxProvider.ContextInfo{}})
	p.SetResultConsumer(recorder)
	p.uiNotifier = slow

	start := time.Now()
	run(p, samplesFor(3))

	deliveryDelay := recorder.delivered.Sub(start)
	if deliveryDelay >= overlayWork {
		t.Errorf("delivery took %v with a %v overlay, so it is waiting on presentation",
			deliveryDelay, overlayWork)
	}
}

// slowNotifier simulates an overlay that takes time to present.
type slowNotifier struct {
	*orderRecorder
	delay time.Duration
}

func (n *slowNotifier) OnFinished(text string) {
	time.Sleep(n.delay)
	n.orderRecorder.OnFinished(text)
}
