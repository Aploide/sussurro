#pragma once

/* Stable native overlay ABI values. Go maps semantic session states to these
 * names explicitly instead of relying on enum ordering or direct casts. */
typedef enum {
    OVERLAY_STATE_IDLE = 0,
    OVERLAY_STATE_RECORDING = 1,
    OVERLAY_STATE_TRANSCRIBING = 2,
    /* Post-recognition work: filler removal, context lookup, and delivery.
     * Shares the transcribing shimmer but must not share its label, since no
     * recognition is running - on the partial-reuse path none ran at all. */
    OVERLAY_STATE_CLEANING_UP = 3,
} OverlayState;
