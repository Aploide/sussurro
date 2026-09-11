// overlay_darwin.m — NSPanel overlay with CoreGraphics drawing
//
// One overlay in every state, matching the GTK backend: a fixed-width panel
// whose bottom edge never moves, carrying a permanent control row (waveform
// and recording-buffer gauge) along that edge, with transcript text growing
// the panel upwards above it. There is no pill-to-panel switch — swapping
// between two shapes is what produced the jolt on Linux, and what made
// anything drawn on the pill vanish the moment text appeared.
#import <Cocoa/Cocoa.h>
#import <QuartzCore/QuartzCore.h>
#include <math.h>
#include <string.h>
#include <stdlib.h>
#include "overlay_state.h"
#include "overlay_palette.h"
#include "overlay_panel.h"

/* Exported Go callbacks — defined by CGo in overlay_darwin.go */
extern void overlayGoOpenSettings(void);
extern void overlayGoQuit(void);

static BOOL g_context_menu_enabled = NO;

/* ------------------------------------------------------------------ */
/* SussurroView — NSView subclass that draws the panel                 */
/* ------------------------------------------------------------------ */

@interface SussurroView : NSView {
@public
    int     state;
    double  animTime;
    double  shimmerPhase;

    float   rmsRing[ITEM_COUNT];
    int     rmsHead;
    double  barHeights[ITEM_COUNT];
    double  barTargets[ITEM_COUNT];

    /* Recording buffer fill, 0 to 1. Smoothed toward its target on the
       animation tick like the bars, so the track glides rather than steps. */
    double  fill;
    double  fillTarget;

    /* Live transcript shown in the panel. Owned by this view and only touched
       on the main thread. */
    NSString *transcript;
    NSString *status;
    BOOL      provisional;   /* text is still being revised */
    BOOL      copied;        /* text has reached the clipboard */
    BOOL      finalizing;    /* final recognition pass is running */

    /* Layout of the current transcript, measured once when it changes rather
       than on every frame. Measuring wrapped text is the expensive part of
       this view, and the display link redraws at the refresh rate: doing it
       in drawRect meant paying for a full re-wrap of the whole transcript
       sixty times a second while the text sat still. */
    double  textHeight;      /* wrapped height at the panel's text width */
    double  textOffset;      /* how far the text is scrolled up past the cap */
    int     measuredHeight;  /* the panel height that text implies */

    /* Attribute dictionaries for the transcript and the status word, built
       once. drawRect runs at the refresh rate, and rebuilding a dictionary
       and a paragraph style sixty times a second to draw the same font is
       pure allocation churn.

       drawnText is the transcript already carrying its colour, rebuilt only
       when the text or the palette changes. Cocoa still lays the string out
       on each draw — only a Core Text frame cached by hand would avoid that —
       but a stable attributed string is what its own layout cache is keyed
       on, and it keeps the per-frame colour mutation out of drawRect. */
    NSMutableDictionary *textAttrs;
    NSMutableDictionary *statusAttrs;
    NSAttributedString  *drawnText;

    /* The height an empty layout occupies: one line. Reserved at every size so
       the panel does not jump when the first partial word arrives, which is
       the jolt the single-shape design exists to avoid. The GTK backend gets
       this for free — Pango reports one line's height for empty text — so
       without it the two backends rest at different heights. */
    double  emptyTextHeight;

    CVDisplayLinkRef displayLink;

    OverlayPalette darkPalette;
    OverlayPalette lightPalette;
    OverlayPalette palette;
    int             themeMode;
}
- (instancetype)initWithFrame:(NSRect)frame
                  darkPalette:(OverlayPalette)dark
                 lightPalette:(OverlayPalette)light;
- (void)setThemeMode:(int)mode
         darkPalette:(OverlayPalette)dark
        lightPalette:(OverlayPalette)light;
- (void)applyResolvedTheme;
- (void)applyState:(int)newState;
- (void)setTranscript:(const char *)text
               status:(const char *)statusText
          provisional:(int)isProvisional
               copied:(int)isCopied
           finalizing:(int)isFinalizing;
- (int)panelHeight;
- (void)remeasure;
- (void)rebuildDrawnText;
- (void)startAnimation;
- (void)stopAnimation;
- (void)tick:(double)dt;
@end

static CVReturn displayLinkCallback(CVDisplayLinkRef link,
                                    const CVTimeStamp *now,
                                    const CVTimeStamp *output,
                                    CVOptionFlags flagsIn,
                                    CVOptionFlags *flagsOut,
                                    void *ctx)
{
    (void)link; (void)now; (void)flagsIn; (void)flagsOut;

    /* The display link fires at the display's refresh rate, which on a
       ProMotion Mac is 120 Hz, not 60. Advancing the animation clock by a
       fixed 1/60 there ran the idle dots and the shimmer at double speed, so
       the interval is taken from the frame the callback is being handed. */
    double dt = 1.0 / 60.0;
    if (output && output->videoTimeScale > 0 && output->videoRefreshPeriod > 0) {
        dt = (double)output->videoRefreshPeriod / (double)output->videoTimeScale;
    }

    SussurroView *v = (__bridge SussurroView *)ctx;
    dispatch_async(dispatch_get_main_queue(), ^{
        [v tick:dt];
    });
    return kCVReturnSuccess;
}

static void set_fill_color(CGContextRef ctx, OverlayColor color, double alphaScale)
{
    CGContextSetRGBFillColor(ctx, color.r, color.g, color.b, color.a * alphaScale);
}

static void set_stroke_color(CGContextRef ctx, OverlayColor color, double alphaScale)
{
    CGContextSetRGBStrokeColor(ctx, color.r, color.g, color.b, color.a * alphaScale);
}

static NSColor *native_color(OverlayColor color, double alphaScale)
{
    return [NSColor colorWithCalibratedRed:color.r green:color.g blue:color.b
                                     alpha:color.a * alphaScale];
}

/* Appends a rounded rectangle, clamping the radius so a shape shorter or
   narrower than its own corners still renders. CGPathAddRoundedRect asserts
   when 2*radius exceeds either side. */
static void add_rounded_rect(CGContextRef ctx, CGRect rect, CGFloat radius)
{
    CGFloat r = radius;
    if (r > rect.size.width  / 2.0) r = rect.size.width  / 2.0;
    if (r > rect.size.height / 2.0) r = rect.size.height / 2.0;
    if (r < 0) r = 0;
    CGMutablePathRef path = CGPathCreateMutable();
    CGPathAddRoundedRect(path, NULL, rect, r, r);
    CGContextAddPath(ctx, path);
    CGPathRelease(path);
}

/* The area the panel is placed and sized within, resolved once per appearance
 * and then held for as long as the panel is up.
 *
 * visibleFrame, not frame: the raw display rect includes the space the Dock
 * occupies, and the panel sits at NSStatusWindowLevel, above the Dock's own
 * level — so measuring from the bottom of the display drew the whole control
 * row on top of the Dock. The work area is the same thing
 * gdk_monitor_get_workarea() gives the GTK backend.
 *
 * mainScreen is the screen holding the window with keyboard focus, which is
 * the one the user is dictating into. Choosing it is a deliberate difference
 * from the Linux backend's fixed primary monitor: on a Mac the overlay appears
 * on the display the work is happening on.
 *
 * Holding it is not optional. Both the panel's position and its height cap
 * come from this rect, and it is consulted on every partial transcript — so
 * re-resolving it live meant that clicking a window on another display
 * mid-dictation teleported the panel there AND re-clamped the cap, scrolling
 * the visible text by hundreds of points in a single frame. The display is
 * picked when the panel comes up and does not change under the user. */
static NSRect g_work_area = {{0, 0}, {1920, 1080}};

static void overlay_resolve_work_area(void)
{
    NSScreen *screen = [NSScreen mainScreen];
    if (!screen) {
        NSArray<NSScreen *> *screens = [NSScreen screens];
        screen = screens.count > 0 ? screens[0] : nil;
    }
    if (screen) g_work_area = [screen visibleFrame];
}

static NSRect overlay_work_area(void)
{
    return g_work_area;
}

/* The tallest the panel may become before it scrolls instead of growing.
   Derived from the screen so a long transcript on a large display uses the
   space available, rather than stopping at a pixel count chosen for a
   smaller one.

   Floored to a whole point: the panel height is rounded up from this, and a
   fractional cap left the text offset and the drawn height disagreeing by up
   to a point. */
static double panel_max_height(void)
{
    double cap = floor(overlay_work_area().size.height * PANEL_MAX_HEIGHT_FRACTION);
    if (cap < PANEL_MIN_MAX_HEIGHT) cap = PANEL_MIN_MAX_HEIGHT;
    return cap;
}

@implementation SussurroView

- (instancetype)initWithFrame:(NSRect)frame
                  darkPalette:(OverlayPalette)dark
                 lightPalette:(OverlayPalette)light
{
    self = [super initWithFrame:frame];
    if (self) {
        state        = OVERLAY_STATE_IDLE;
        darkPalette  = dark;
        lightPalette = light;
        themeMode    = OVERLAY_THEME_SYSTEM;
        palette      = dark;
        animTime     = 0.0;
        shimmerPhase = 0.0;
        rmsHead      = 0;
        fill         = 0.0;
        fillTarget   = 0.0;
        transcript   = nil;
        status       = nil;
        textOffset   = 0.0;
        drawnText    = nil;

        NSMutableParagraphStyle *wrap =
            [[NSParagraphStyle defaultParagraphStyle] mutableCopy];
        wrap.lineBreakMode = NSLineBreakByWordWrapping;
        textAttrs = [[NSMutableDictionary alloc] initWithObjectsAndKeys:
            [NSFont systemFontOfSize:PANEL_TEXT_SIZE], NSFontAttributeName,
            wrap, NSParagraphStyleAttributeName, nil];
        [wrap release];
        statusAttrs = [[NSMutableDictionary alloc] initWithObjectsAndKeys:
            [NSFont systemFontOfSize:PANEL_STATUS_SIZE weight:NSFontWeightMedium],
            NSFontAttributeName, nil];

        /* Measured through the same call the transcript is measured with, so
           a one-line transcript comes out at exactly this height and the
           panel does not move when the first word lands. */
        emptyTextHeight = ceil([@" "
            boundingRectWithSize:NSMakeSize(PANEL_WIDTH - 2 * PANEL_PAD_X, 1.0e7)
                         options:NSStringDrawingUsesLineFragmentOrigin
                      attributes:textAttrs].size.height);
        textHeight     = emptyTextHeight;
        measuredHeight = (int)ceil(PANEL_PAD_Y * 2 + ROW_HEIGHT +
                                   emptyTextHeight + TEXT_ROW_GAP);
        for (int i = 0; i < ITEM_COUNT; i++) {
            barHeights[i] = BAR_MIN_HEIGHT;
            barTargets[i] = BAR_MIN_HEIGHT;
        }

        CVDisplayLinkCreateWithActiveCGDisplays(&displayLink);
        CVDisplayLinkSetOutputCallback(displayLink, displayLinkCallback,
                                       (__bridge void *)self);
        /* Deliberately not started here: the overlay is created hidden, and
           redrawing a window nobody can see is pure waste. startAnimation
           runs when it is shown. */
    }
    return self;
}

/* Flipped, so this port reads the same way as the cairo original it mirrors:
   the origin is top-left and y grows downwards in both. */
- (BOOL)isFlipped { return YES; }

- (BOOL)systemIsDark
{
    NSString *match = [NSApp.effectiveAppearance bestMatchFromAppearancesWithNames:@[
        NSAppearanceNameDarkAqua, NSAppearanceNameAqua
    ]];
    return [match isEqualToString:NSAppearanceNameDarkAqua];
}

- (void)applyResolvedTheme
{
    NSVisualEffectView *effect = [self.superview isKindOfClass:[NSVisualEffectView class]]
        ? (NSVisualEffectView *)self.superview : nil;
    BOOL followsSystem = themeMode == OVERLAY_THEME_SYSTEM;
    if (followsSystem) {
        /* An explicit appearance stops AppKit propagating later system changes
           to the view. Clear it before resolving System mode. */
        effect.appearance = nil;
    }

    BOOL dark = themeMode == OVERLAY_THEME_DARK ||
        (followsSystem && [self systemIsDark]);
    palette = dark ? darkPalette : lightPalette;
    if (!followsSystem) {
        effect.appearance = [NSAppearance appearanceNamed:
            dark ? NSAppearanceNameVibrantDark : NSAppearanceNameVibrantLight];
    }
    self.window.hasShadow = dark ? NO : YES;
    /* The transcript carries its colour from the palette, so a theme change
       has to rebuild it or the text keeps the old theme's colour. */
    [self rebuildDrawnText];
    [self setNeedsDisplay:YES];
}

- (void)setThemeMode:(int)mode
         darkPalette:(OverlayPalette)dark
        lightPalette:(OverlayPalette)light
{
    themeMode = mode;
    darkPalette = dark;
    lightPalette = light;
    [self applyResolvedTheme];
}

- (void)viewDidChangeEffectiveAppearance
{
    [super viewDidChangeEffectiveAppearance];
    if (themeMode == OVERLAY_THEME_SYSTEM) [self applyResolvedTheme];
}

- (void)dealloc
{
    [self stopAnimation];
    CVDisplayLinkRelease(displayLink);
    [transcript release];
    [status release];
    [textAttrs release];
    [statusAttrs release];
    [drawnText release];
    [super dealloc];
}

- (void)startAnimation
{
    if (displayLink && !CVDisplayLinkIsRunning(displayLink)) {
        CVDisplayLinkStart(displayLink);
    }
}

- (void)stopAnimation
{
    if (displayLink && CVDisplayLinkIsRunning(displayLink)) {
        CVDisplayLinkStop(displayLink);
    }
}

- (void)tick:(double)dt
{
    animTime     += dt;
    shimmerPhase += dt;
    for (int i = 0; i < ITEM_COUNT; i++) {
        barHeights[i] = barHeights[i] * 0.7 + barTargets[i] * 0.3;
    }
    fill = fill * 0.9 + fillTarget * 0.1;
    [self setNeedsDisplay:YES];
}

/* Applies a state change, clearing the buffer-fill gauge on entry to
 * RECORDING: that starts a fresh buffer, so the fill is reset rather than
 * letting the smoothing drag the previous recording's value down across the
 * first second of the new one. */
- (void)applyState:(int)newState
{
    if (newState == OVERLAY_STATE_RECORDING && state != OVERLAY_STATE_RECORDING) {
        fill       = 0.0;
        fillTarget = 0.0;
    }
    state = newState;
}

- (void)setTranscript:(const char *)text
               status:(const char *)statusText
          provisional:(int)isProvisional
               copied:(int)isCopied
           finalizing:(int)isFinalizing
{
    NSString *newText = (text && text[0])
        ? [[NSString alloc] initWithUTF8String:text] : nil;
    NSString *newStatus = (statusText && statusText[0])
        ? [[NSString alloc] initWithUTF8String:statusText] : nil;

    [transcript release];
    [status release];
    transcript  = newText;
    status      = newStatus;
    provisional = isProvisional ? YES : NO;
    copied      = isCopied ? YES : NO;
    finalizing  = isFinalizing ? YES : NO;

    [self remeasure];
}

- (BOOL)isOpaque { return NO; }
- (BOOL)wantsLayer { return YES; }

- (void)rightMouseDown:(NSEvent *)event
{
    if (!g_context_menu_enabled) return;
    NSMenu *menu = [[[NSMenu alloc] initWithTitle:@""] autorelease];
    [menu addItemWithTitle:@"Open Settings"
                   action:@selector(menuOpenSettings)
            keyEquivalent:@""];
    [menu addItem:[NSMenuItem separatorItem]];
    [menu addItemWithTitle:@"Quit"
                   action:@selector(menuQuit)
            keyEquivalent:@""];
    for (NSMenuItem *item in menu.itemArray) {
        item.target = self;
    }
    [NSApp activateIgnoringOtherApps:YES];
    [self.window makeKeyWindow];
    [NSMenu popUpContextMenu:menu withEvent:event forView:self];
}

- (void)menuOpenSettings { overlayGoOpenSettings(); }
- (void)menuQuit         { overlayGoQuit(); }

/* ---- Transcript text ---- */

/* Measures the transcript and caches the panel geometry it implies.
 *
 * Past the cap the panel stops growing and the text is anchored to its END
 * rather than its start: during dictation the newest words matter, and drawing
 * from the top would leave them off the bottom edge, which is the cropping
 * reported in sussurro-xvj.48. */
- (void)remeasure
{
    textHeight = emptyTextHeight;
    if (transcript.length > 0) {
        NSRect measured = [transcript
            boundingRectWithSize:NSMakeSize(PANEL_WIDTH - 2 * PANEL_PAD_X, 1.0e7)
                         options:NSStringDrawingUsesLineFragmentOrigin
                      attributes:textAttrs];
        textHeight = ceil(measured.size.height);
        if (textHeight < emptyTextHeight) textHeight = emptyTextHeight;
    }

    /* The control row and one line of text space are both permanent, so both
       are part of the panel's height at every size. */
    double height = PANEL_PAD_Y * 2 + ROW_HEIGHT + textHeight + TEXT_ROW_GAP;

    textOffset = 0.0;
    double cap = panel_max_height();
    if (height > cap) {
        textOffset = height - cap;
        height = cap;
    }
    measuredHeight = (int)ceil(height);

    [self rebuildDrawnText];
}

/* Rebuilds the coloured transcript. Called whenever the text or the palette
   changes — the colour says whether the text is still being revised, being
   settled, or already copied, so it follows both. */
- (void)rebuildDrawnText
{
    [drawnText release];
    drawnText = nil;
    if (transcript.length == 0) return;

    /* Provisional text is dimmed: it is still being revised, and the user
       should be able to tell settled text from text that may still change
       under them. */
    OverlayColor color;
    double alpha = 1.0;
    if (finalizing) {
        color = palette.finalizing;
    } else if (provisional) {
        color = palette.provisional;
    } else if (copied) {
        color = palette.copied;
    } else {
        color = palette.primary;
        alpha = 0.95;
    }

    textAttrs[NSForegroundColorAttributeName] = native_color(color, alpha);
    drawnText = [[NSAttributedString alloc] initWithString:transcript
                                               attributes:textAttrs];
}

- (int)panelHeight
{
    return measuredHeight;
}

/* ---- Drawing ---- */

- (void)drawRect:(NSRect)dirtyRect
{
    (void)dirtyRect;

    CGContextRef ctx = [[NSGraphicsContext currentContext] CGContext];
    NSRect bounds = self.bounds;
    double w = bounds.size.width;
    double h = bounds.size.height;

    CGContextClearRect(ctx, bounds);

    add_rounded_rect(ctx, CGRectMake(0, 0, w, h), PANEL_RADIUS);
    set_fill_color(ctx, palette.background, 1.0);
    CGContextFillPath(ctx);

    /* Inset the border by half its stroke width so the panel mask does not
       clip it. */
    CGFloat inset = 0.5;
    add_rounded_rect(ctx,
        CGRectMake(inset, inset, w - inset * 2, h - inset * 2),
        PANEL_RADIUS - inset);
    set_stroke_color(ctx, palette.border, 1.0);
    CGContextSetLineWidth(ctx, 1.0);
    CGContextStrokePath(ctx);

    if (drawnText.length > 0) {
        [self drawTranscript:ctx panelHeight:h];
    }

    [self drawControlRow:ctx width:w panelHeight:h];
}

- (void)drawTranscript:(CGContextRef)ctx panelHeight:(double)panelH
{
    /* Clip the text to its own region, which stops above the control row. A
       scrolled layout starts above the panel's top edge, so without this it
       would paint over the window's surroundings, and its last lines would
       run underneath the row. */
    CGContextSaveGState(ctx);
    CGContextClipToRect(ctx, CGRectMake(0, 0, PANEL_WIDTH,
        panelH - PANEL_PAD_Y - ROW_HEIGHT - TEXT_ROW_GAP));
    [drawnText drawWithRect:NSMakeRect(PANEL_PAD_X,
                                       PANEL_PAD_Y - textOffset,
                                       PANEL_WIDTH - 2 * PANEL_PAD_X,
                                       textHeight)
                    options:NSStringDrawingUsesLineFragmentOrigin];
    CGContextRestoreGState(ctx);
}

/* Draws the overlay's permanent bottom row: waveform on the left, buffer-fill
 * gauge filling the rest. Drawn in every state, so neither element disappears
 * when text arrives. */
- (void)drawControlRow:(CGContextRef)ctx width:(double)width panelHeight:(double)panelH
{
    double contentW = width - 2.0 * PANEL_PAD_X;
    double waveW    = contentW * ROW_WAVEFORM_FRACTION;
    double gaugeW   = contentW - waveW - ROW_GAP;
    double rowTop   = panelH - PANEL_PAD_Y - ROW_HEIGHT;
    double centreY  = rowTop + ROW_HEIGHT / 2.0;

    /* State decides first, and RECORDING always wins.
     *
     * The waveform is the only live confirmation that audio is being captured,
     * so nothing may displace it while recording is running. Testing the status
     * string ahead of the state let any label blank the waveform mid-dictation
     * (sussurro-xvj.61).
     *
     * Once recording stops the waveform means nothing, and the slot carries a
     * status word instead: it says the overlay is working rather than hung, and
     * confirms the copy. */
    if (state == OVERLAY_STATE_RECORDING) {
        [self drawBars:ctx x:PANEL_PAD_X w:waveW centreY:centreY];
    } else if (status.length > 0) {
        [self drawRowStatus:ctx x:PANEL_PAD_X centreY:centreY];
    } else if (state == OVERLAY_STATE_IDLE) {
        [self drawDots:ctx x:PANEL_PAD_X w:waveW centreY:centreY];
    } else {
        [self drawBars:ctx x:PANEL_PAD_X w:waveW centreY:centreY];
    }

    /* The gauge is vertically centred in the row rather than sitting on the
       overlay's edge, so it stays aligned with the waveform beside it. */
    [self drawBufferFill:ctx
                       x:PANEL_PAD_X + waveW + ROW_GAP
                       y:centreY - FILL_TRACK_HEIGHT / 2.0
                       w:gaugeW];
}

- (void)drawDots:(CGContextRef)ctx x:(double)x w:(double)w centreY:(double)centreY
{
    double totalW = (ITEM_COUNT - 1) * DOT_SPACING;
    double startX = x + (w - totalW) / 2.0;

    for (int i = 0; i < ITEM_COUNT; i++) {
        double phi = 2.0 * M_PI * animTime / 4.0 + i * 2.0 * M_PI / (double)ITEM_COUNT;
        double s   = sin(phi);
        double a   = 0.35 + 0.65 * s * s;
        double cx  = startX + i * DOT_SPACING;
        set_fill_color(ctx, palette.primary, a);
        CGContextFillEllipseInRect(ctx,
            CGRectMake(cx - DOT_RADIUS, centreY - DOT_RADIUS,
                       DOT_RADIUS * 2, DOT_RADIUS * 2));
    }
}

/* The bars occupy a fixed width, so at the wide slot of the unified layout
   they are centred in the space rather than stretched across it: the waveform
   is a liveness indicator here, and thinner bars spread wider would read as
   less, not more. */
- (void)drawBars:(CGContextRef)ctx x:(double)x w:(double)w centreY:(double)centreY
{
    double totalW = (ITEM_COUNT - 1) * BAR_SPACING;
    double startX = x + (w - totalW) / 2.0;

    set_fill_color(ctx, palette.primary, 1.0);
    for (int i = 0; i < ITEM_COUNT; i++) {
        double bh = barHeights[i];
        double cx = startX + i * BAR_SPACING;
        add_rounded_rect(ctx,
            CGRectMake(cx - BAR_WIDTH / 2.0, centreY - bh / 2.0, BAR_WIDTH, bh),
            BAR_RADIUS);
        CGContextFillPath(ctx);
    }
}

/* Draws the recording buffer fill.
 *
 * It lives in the overlay's permanent bottom row: it must stay visible while
 * text is on screen, which is exactly when a long dictation risks reaching the
 * cap. The track turns warning-coloured past FILL_WARN_FRACTION, so the
 * approach to a truncating cap reads at a glance without needing a number. */
- (void)drawBufferFill:(CGContextRef)ctx x:(double)x y:(double)y w:(double)w
{
    /* Unfilled track: dim enough to read as a groove rather than content. */
    set_fill_color(ctx, palette.track, 1.0);
    add_rounded_rect(ctx, CGRectMake(x, y, w, FILL_TRACK_HEIGHT),
                     FILL_TRACK_HEIGHT / 2.0);
    CGContextFillPath(ctx);

    double fillW = w * fill;
    if (fillW <= 0.0) return;
    /* Never narrower than the cap it is drawn with, or the rounded ends
       degenerate into a dot at very low fill. */
    if (fillW < FILL_TRACK_HEIGHT) fillW = FILL_TRACK_HEIGHT;

    if (fill >= FILL_WARN_FRACTION) {
        set_fill_color(ctx, palette.warning, 1.0);
    } else {
        set_fill_color(ctx, palette.fill, 1.0);
    }
    add_rounded_rect(ctx, CGRectMake(x, y, fillW, FILL_TRACK_HEIGHT),
                     FILL_TRACK_HEIGHT / 2.0);
    CGContextFillPath(ctx);
}

/* Draws the status word in the control row's left slot, vertically centred and
 * left-aligned with the transcript text above it.
 *
 * The word is allowed to overrun its slot: "Finalizing" is wider than the
 * waveform's 10%, and the gauge beside it is a bar with no content to collide
 * with, so overlapping it slightly is preferable to truncating the word.
 *
 * While post-recording work runs, a gleam sweeps across the word. That is the
 * one thing on screen saying the overlay is working rather than hung, and a
 * static label says it far less convincingly. */
- (void)drawRowStatus:(CGContextRef)ctx x:(double)x centreY:(double)centreY
{
    BOOL working = state == OVERLAY_STATE_TRANSCRIBING ||
                   state == OVERLAY_STATE_CLEANING_UP;
    statusAttrs[NSForegroundColorAttributeName] =
        native_color(working ? palette.shimmer_base : palette.secondary, 1.0);
    NSSize size = [status sizeWithAttributes:statusAttrs];
    NSPoint origin = NSMakePoint(floor(x), floor(centreY - size.height / 2.0));

    if (!working) {
        [status drawAtPoint:origin withAttributes:statusAttrs];
        return;
    }

    /* Transparency layer so SourceIn clips the gradient strictly to text ink.
       Bounded to the word: the no-rect form takes the current clip, which the
       animation tick makes the entire panel, so every frame allocated an
       860pt-wide offscreen buffer to shimmer a word sixty points across. */
    CGContextBeginTransparencyLayerWithRect(ctx,
        CGRectMake(origin.x, origin.y, size.width, size.height), NULL);
    [status drawAtPoint:origin withAttributes:statusAttrs];

    CGContextSaveGState(ctx);
    CGContextSetBlendMode(ctx, kCGBlendModeSourceIn);

    double bandW  = size.width * 0.85;             /* band ~85% of text width */
    double travel = size.width + bandW;            /* enter fully, exit fully */
    double phase  = fmod(shimmerPhase / 2.0, 1.0); /* 2-second cycle          */
    double cx     = origin.x - bandW * 0.5 + travel * phase;

    /* Gradient stops: flat-zero -> gentle rise -> bright peak -> gentle fall
       -> flat-zero. Keeping the bright zone narrow at the centre gives the
       "light gleam" feel. */
    NSGradient *grad = [[[NSGradient alloc]
        initWithColors:@[
            native_color(palette.shimmer_peak, 0.0),
            native_color(palette.shimmer_peak, 0.0),
            native_color(palette.shimmer_peak, 1.0),
            native_color(palette.shimmer_peak, 0.0),
            native_color(palette.shimmer_peak, 0.0)
        ]
        atLocations:(CGFloat[]){0.0, 0.2, 0.5, 0.8, 1.0}
        colorSpace:[NSColorSpace genericRGBColorSpace]] autorelease];

    [grad drawInRect:NSMakeRect(cx - bandW * 0.5, origin.y, bandW, size.height)
               angle:0];

    CGContextRestoreGState(ctx);
    CGContextEndTransparencyLayer(ctx);
}

@end

/* ------------------------------------------------------------------ */
/* SussurroPanel — NSPanel subclass                                    */
/* ------------------------------------------------------------------ */

@interface SussurroPanel : NSPanel
@end
@implementation SussurroPanel
- (BOOL)canBecomeKeyWindow { return YES; }
- (BOOL)canBecomeMainWindow { return NO; }
@end

/* ------------------------------------------------------------------ */
/* C-linkage API                                                       */
/* ------------------------------------------------------------------ */

static SussurroPanel       *g_panel = nil;
static SussurroView        *g_view  = nil;
static NSVisualEffectView  *g_blur  = nil;

/* Clips the blur strictly to the panel silhouette so it does not bleed outside
   the rounded corners. Re-applied on every resize, because the mask is a fixed
   path rather than something the layer recomputes. */
static void update_panel_mask(NSSize size)
{
    if (!g_blur) return;
    CAShapeLayer *mask = (CAShapeLayer *)g_blur.layer.mask;
    if (!mask) return;
    /* A hand-created layer does not inherit its host's backing scale, so
       without this the rounded corners are masked at 1x and look ragged on a
       Retina display. */
    mask.contentsScale = g_blur.layer.contentsScale;
    CGPathRef path = CGPathCreateWithRoundedRect(
        CGRectMake(0, 0, size.width, size.height),
        PANEL_RADIUS, PANEL_RADIUS, NULL);
    /* Without this the implicit layer animation lags the window resize by a
       fraction of a second, which shows as a clipped corner while text grows. */
    [CATransaction begin];
    [CATransaction setDisableActions:YES];
    mask.path = path;
    [CATransaction commit];
    CGPathRelease(path);
}

/* Keeps the overlay bottom-centred on the main screen at its current height.
   The window changes size as the transcript grows, so the frame is recomputed
   rather than set once at creation — and because macOS window origins are
   bottom-left, holding the origin still is exactly what makes the panel grow
   upwards. */
static void overlay_apply_geometry(void)
{
    if (!g_panel || !g_view) return;

    double height = (double)[g_view panelHeight];
    /* The same area panel_max_height() caps against, so the height the view
       measured and the frame it is given can never disagree. */
    NSRect area = overlay_work_area();
    NSRect target = NSMakeRect(
        area.origin.x + floor((area.size.width - PANEL_WIDTH) / 2.0),
        area.origin.y + OVERLAY_BOTTOM_MARGIN,
        PANEL_WIDTH, height);

    if (NSEqualRects(g_panel.frame, target)) return;

    [g_panel setFrame:target display:YES];
    update_panel_mask(target.size);
}

void* overlay_create_macos(const OverlayPalette *dark_palette,
                           const OverlayPalette *light_palette)
{
    /* The overlay is created before the webview, so initialise AppKit before
       consulting NSApp.effectiveAppearance. */
    [NSApplication sharedApplication];
    OverlayPalette dark = *dark_palette;
    OverlayPalette light = *light_palette;
    overlay_resolve_work_area();
    NSRect area  = overlay_work_area();
    NSRect frame = NSMakeRect(
        area.origin.x + floor((area.size.width - PANEL_WIDTH) / 2.0),
        area.origin.y + OVERLAY_BOTTOM_MARGIN,
        PANEL_WIDTH, OVERLAY_REST_HEIGHT);

    g_panel = [[SussurroPanel alloc]
        initWithContentRect:frame
                  styleMask:NSWindowStyleMaskBorderless
                    backing:NSBackingStoreBuffered
                      defer:NO];

    g_panel.level                    = NSStatusWindowLevel;
    g_panel.opaque                   = NO;
    g_panel.hasShadow                = NO;
    g_panel.hidesOnDeactivate        = NO;
    g_panel.backgroundColor          = [NSColor clearColor];
    g_panel.collectionBehavior =
        NSWindowCollectionBehaviorCanJoinAllSpaces |
        NSWindowCollectionBehaviorStationary       |
        NSWindowCollectionBehaviorIgnoresCycle     |
        NSWindowCollectionBehaviorFullScreenAuxiliary;

    NSRect viewRect = NSMakeRect(0, 0, frame.size.width, frame.size.height);

    /* NSVisualEffectView — real OS-level blur of whatever is behind the
       window. */
    g_blur = [[NSVisualEffectView alloc] initWithFrame:viewRect];
    g_blur.material     = NSVisualEffectMaterialHUDWindow;
    g_blur.blendingMode = NSVisualEffectBlendingModeBehindWindow;
    g_blur.state        = NSVisualEffectStateActive;
    g_blur.wantsLayer   = YES;
    g_blur.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
    g_blur.layer.mask   = [CAShapeLayer layer];

    g_view = [[SussurroView alloc] initWithFrame:viewRect
                                     darkPalette:dark
                                    lightPalette:light];
    g_view.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
    [g_blur addSubview:g_view];
    [g_panel setContentView:g_blur];
    update_panel_mask(viewRect.size);
    [g_view applyResolvedTheme];

    /* Deliberately not shown here. The panel is ordered front only while
       something is happening (see overlay_show_macos), so an idle Sussurro
       leaves nothing on screen. */

    return (__bridge void *)g_panel;
}

void overlay_set_state_macos(int state)
{
    dispatch_async(dispatch_get_main_queue(), ^{
        if (!g_view) return;
        [g_view applyState:state];
        [g_view setNeedsDisplay:YES];
    });
}

/* Applies state, transcript and status in a single main-thread callback.
   Setting them through separate calls lets the run loop draw between the two,
   showing a state that no longer matches the text beside it. */
void overlay_present_macos(int state, const char *text, const char *status,
                           int provisional, int copied, int finalizing)
{
    char *text_copy   = text   ? strdup(text)   : NULL;
    char *status_copy = status ? strdup(status) : NULL;
    dispatch_async(dispatch_get_main_queue(), ^{
        if (g_view) {
            [g_view applyState:state];
            [g_view setTranscript:text_copy
                           status:status_copy
                      provisional:provisional
                           copied:copied
                       finalizing:finalizing];
            overlay_apply_geometry();
            [g_view setNeedsDisplay:YES];
        }
        free(text_copy);
        free(status_copy);
    });
}

void overlay_push_rms_macos(float rms)
{
    dispatch_async(dispatch_get_main_queue(), ^{
        if (!g_view) return;
        g_view->rmsRing[g_view->rmsHead] = rms;
        g_view->rmsHead = (g_view->rmsHead + 1) % ITEM_COUNT;
        for (int i = 0; i < ITEM_COUNT; i++) {
            int idx = (g_view->rmsHead + i) % ITEM_COUNT;
            float v = g_view->rmsRing[idx];
            double norm = v / RMS_SCALE;
            if (norm > 1.0) norm = 1.0;
            g_view->barTargets[i] = BAR_MIN_HEIGHT +
                                    norm * (BAR_MAX_HEIGHT - BAR_MIN_HEIGHT);
        }
    });
}

void overlay_push_fill_macos(double fill)
{
    dispatch_async(dispatch_get_main_queue(), ^{
        if (!g_view) return;
        double clamped = fill;
        if (clamped < 0.0) clamped = 0.0;
        if (clamped > 1.0) clamped = 1.0;
        g_view->fillTarget = clamped;
    });
}

void overlay_set_theme_macos(int mode,
                             const OverlayPalette *dark_palette,
                             const OverlayPalette *light_palette)
{
    OverlayPalette dark = *dark_palette;
    OverlayPalette light = *light_palette;
    dispatch_async(dispatch_get_main_queue(), ^{
        if (g_view) [g_view setThemeMode:mode darkPalette:dark lightPalette:light];
    });
}

void overlay_show_macos(void)
{
    dispatch_async(dispatch_get_main_queue(), ^{
        /* Coming back from hidden is where the display is chosen, and where
           any change to it since last time — unplugged, resized, or the user
           now working on a different screen — is picked up. The cap follows
           the work area, so the panel has to be measured again against it.

           A panel already on screen keeps both: its display must not move
           under the user, and it was measured by the present that put the
           text there, so measuring the whole transcript again on every show
           is exactly what the cache exists to avoid. */
        if (!g_panel.isVisible) {
            overlay_resolve_work_area();
            [g_view remeasure];
        }
        overlay_apply_geometry();
        [g_view startAnimation];
        [g_panel orderFrontRegardless];
    });
}

void overlay_hide_macos(void)
{
    dispatch_async(dispatch_get_main_queue(), ^{
        /* Redrawing a hidden window is pure waste, and the display link runs
           at the refresh rate whether or not anything can see it. */
        [g_view stopAnimation];
        [g_panel orderOut:nil];
    });
}

void overlay_set_context_menu_callbacks_macos(void)
{
    dispatch_async(dispatch_get_main_queue(), ^{
        g_context_menu_enabled = YES;
    });
}

/* Tear down the overlay cleanly and terminate the process without running
   C++ global destructors (which trigger a Metal render-encoder assertion
   inside whisper.cpp's ggml-metal when called via os.Exit -> C exit()). */
void overlay_terminate_macos(void)
{
    if (g_view) {
        [g_view stopAnimation];
    }
    if (g_panel) {
        [g_panel orderOut:nil];
    }
    /* _exit() skips atexit/C++ destructors, preventing the Metal assertion. */
    _exit(0);
}
