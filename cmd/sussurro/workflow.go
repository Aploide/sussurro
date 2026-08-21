package main

import (
	"log/slog"
	"os"

	"github.com/aploide/sussurro/internal/clipboard"
	"github.com/aploide/sussurro/internal/config"
	"github.com/aploide/sussurro/internal/delivery"
	"github.com/aploide/sussurro/internal/llm"
	"github.com/aploide/sussurro/internal/pipeline"
	"github.com/aploide/sussurro/internal/review"
	"github.com/aploide/sussurro/internal/session"
	"github.com/aploide/sussurro/internal/ui"
)

// workflow is the wiring the running application needs, whichever interaction
// mode is configured. Immediate mode leaves controller nil.
type workflow struct {
	// dispatch receives recording gestures from every input source.
	dispatch session.InputDispatcher
	// controller is the review state machine, or nil in immediate mode.
	controller *session.Controller
	// partial receives partial transcriptions, or nil when they are only logged.
	partial func(generation uint64, text string)
}

// buildWorkflow wires the configured interaction mode onto the pipeline.
//
// Immediate mode installs the compatibility delivery consumer and the plain
// recorder dispatcher, exactly as before. Review mode installs the session
// controller as both the result consumer and the input dispatcher, so hotkeys,
// the trigger socket, and evdev all drive the same state machine.
func buildWorkflow(
	cfg *config.Config,
	pipe *pipeline.Pipeline,
	llmEngine *llm.Engine,
	injector delivery.Injector,
	present func(model ui.ViewModel),
	log *slog.Logger,
) workflow {
	if !cfg.Workflow.ReviewEnabled() {
		// Clipboard-only means the paste keystroke is skipped; the text is
		// still staged and echoed exactly as before.
		if cfg.Workflow.ClipboardOnlyDelivery() {
			injector = nil
			log.Info("Delivery is clipboard-only; text will not be pasted automatically")
		}
		installImmediateDelivery(pipe, injector, log)
		return workflow{dispatch: session.NewImmediateDispatcher(pipe)}
	}

	backend, err := selectDeliveryBackend(cfg, injector, log)
	if err != nil {
		// Losing delivery entirely would make review mode a dead end, so fall
		// back to dictation that still works.
		log.Error("Review mode unavailable, falling back to immediate", "error", err)
		installImmediateDelivery(pipe, injector, log)
		return workflow{dispatch: session.NewImmediateDispatcher(pipe)}
	}

	// The controller is referenced by the adapters it owns, so it is declared
	// before them and captured by the closures below.
	var controller *session.Controller

	recognizer := pipeline.NewSessionRecognizer(pipe, func(id session.SessionID, text string) {
		controller.OnResult(id, text)
	}, log)

	editor := review.NewEditor(llmEngine, func(id session.SessionID, text string) {
		controller.OnEdited(id, text)
	}, log)

	presenter := ui.NewReviewPresenter(present)

	controller = session.NewController(
		recognizer,
		editor,
		delivery.NewDeliverer(backend, nil),
		presenter,
		log,
	)

	pipe.SetResultConsumer(recognizer)
	log.Info("Review mode enabled", "delivery", backend.Name())

	return workflow{
		dispatch:   controller,
		controller: controller,
		partial: func(generation uint64, text string) {
			controller.OnPartial(session.SessionID(generation), text)
		},
	}
}

// installImmediateDelivery wires upstream's deliver-on-completion behaviour.
func installImmediateDelivery(pipe *pipeline.Pipeline, injector delivery.Injector, log *slog.Logger) {
	immediate := delivery.NewImmediate(clipboard.Write, injector, os.Stdout, log)
	pipe.SetResultConsumer(pipeline.ResultConsumerFunc(func(result pipeline.Result) {
		if result.Empty() {
			return
		}
		if err := immediate.Deliver(result.Text); err != nil {
			log.Error("Immediate delivery failed", "error", err)
		}
	}))
}

// selectDeliveryBackend resolves the configured delivery backend, using
// clipboard paste as the portable fallback.
func selectDeliveryBackend(cfg *config.Config, injector delivery.Injector, log *slog.Logger) (delivery.Backend, error) {
	var clipboardBackend delivery.Backend
	if injector != nil {
		clipboardBackend = delivery.NewClipboardBackend(clipboard.Write, injector, nil)
	}

	backend, err := delivery.SelectBackend(
		delivery.BackendName(cfg.Workflow.Delivery.Backend),
		delivery.Capabilities{
			Clipboard:      clipboardBackend,
			ClipboardWrite: clipboard.Write,
		},
	)
	if err != nil {
		return nil, err
	}

	if cfg.Workflow.ClipboardOnlyDelivery() {
		log.Info("Delivery is clipboard-only; text will not be pasted automatically")
	}
	return backend, nil
}
