#pragma once

/* Geometry of the unified overlay panel, shared by every native backend that
 * draws it.
 *
 * There is one overlay in every state, not a capsule that swaps for a panel:
 * the window keeps a constant width and a bottom edge that does not move, and
 * text grows the height upwards from the resting size and shrinks it back as
 * text clears.
 *
 * These values were tuned once, on Linux, against real dictation. They live
 * here rather than in each backend's own header so the macOS panel cannot
 * drift from the GTK one — a second copy of PANEL_WIDTH is a second thing to
 * forget when one of them changes. */

/* ---- Geometry ---- */
#define PANEL_WIDTH      860
#define PANEL_PAD_X       22
#define PANEL_PAD_Y       18
#define PANEL_TEXT_SIZE   17
#define PANEL_STATUS_SIZE 12
/* Corner radius of the panel itself. */
#define PANEL_RADIUS      12.0
/* Fraction of the monitor height the panel may occupy before it stops growing
   and scrolls instead. A fixed pixel cap was the cause of sussurro-xvj.48:
   past it the window stopped rising while the text kept going, so the overflow
   ran off the bottom edge and read as the panel growing downwards. */
#define PANEL_MAX_HEIGHT_FRACTION 0.6
/* Floor for the above, for a very short screen or an unreadable monitor size. */
#define PANEL_MIN_MAX_HEIGHT 320
#define ITEM_COUNT         7
/* Gap between the overlay's bottom edge and the bottom of the screen. */
#define OVERLAY_BOTTOM_MARGIN 24

/* ---- Bar parameters ---- */
#define BAR_WIDTH       5.0
#define BAR_RADIUS      2.5
#define BAR_SPACING     8.0
#define BAR_MIN_HEIGHT  4.0
#define BAR_MAX_HEIGHT 40.0
#define RMS_SCALE       0.08

/* ---- Bottom control row ---- */
/* Waveform and buffer-fill share one row anchored to the overlay's bottom
 * edge, which is the only position that stays put as text grows upwards
 * (sussurro-xvj.48). Both are permanent: anything drawn only on the pill
 * vanished the moment text arrived, which is what made the buffer-fill
 * indicator useless during the long dictations it exists to warn about.
 *
 * The gauge takes the larger share because it is what carries information over
 * a long dictation; the waveform only has to show that capture is live. */
#define ROW_WAVEFORM_FRACTION 0.10
#define ROW_GAP               14.0
#define ROW_HEIGHT            44.0
/* Breathing room between the transcript and the control row below it. Text
   sitting directly on the row read as cramped. */
#define TEXT_ROW_GAP          14
/* The overlay's resting height: the control row plus its padding, with no text.
   Text grows the panel upwards from here. */
#define OVERLAY_REST_HEIGHT   (int)(ROW_HEIGHT + 2 * PANEL_PAD_Y)

/* ---- Recording buffer fill indicator ---- */
#define FILL_TRACK_HEIGHT    5.0
/* Past this fraction the fill turns warning-coloured. */
#define FILL_WARN_FRACTION   0.8

/* ---- Dot parameters ---- */
#define DOT_RADIUS   3.0
#define DOT_SPACING 10.0
