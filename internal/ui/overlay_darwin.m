// overlay_darwin.m — NSPanel overlay with CoreGraphics drawing
#import <Cocoa/Cocoa.h>
#import <QuartzCore/QuartzCore.h>
#include <math.h>
#include "overlay_state.h"

/* Exported Go callbacks — defined by CGo in overlay_darwin.go */
extern void overlayGoOpenSettings(void);
extern void overlayGoQuit(void);

static BOOL g_context_menu_enabled = NO;

#define ITEM_COUNT     7
#define BAR_MIN_HEIGHT 4.0
#define BAR_MAX_HEIGHT 40.0
#define RMS_SCALE      0.08

typedef void (*HotkeyDownCB)(void);
typedef void (*HotkeyUpCB)(void);

/* ------------------------------------------------------------------ */
/* SussurroView — NSView subclass that draws the pill                  */
/* ------------------------------------------------------------------ */

@interface SussurroView : NSView {
@public
    int     state;
    double  animTime;
    double  shimmerPhase;

    float   rmsRing[7];
    int     rmsHead;
    double  barHeights[7];
    double  barTargets[7];

    CVDisplayLinkRef displayLink;
}
- (void)tick:(double)dt;
@end

static CVReturn displayLinkCallback(CVDisplayLinkRef link,
                                    const CVTimeStamp *now,
                                    const CVTimeStamp *output,
                                    CVOptionFlags flagsIn,
                                    CVOptionFlags *flagsOut,
                                    void *ctx)
{
    (void)link; (void)now; (void)output; (void)flagsIn; (void)flagsOut;
    SussurroView *v = (__bridge SussurroView *)ctx;
    dispatch_async(dispatch_get_main_queue(), ^{
        [v tick:1.0/60.0];
    });
    return kCVReturnSuccess;
}

@implementation SussurroView

- (instancetype)initWithFrame:(NSRect)frame
{
    self = [super initWithFrame:frame];
    if (self) {
        state        = OVERLAY_STATE_IDLE;
        animTime     = 0.0;
        shimmerPhase = 0.0;
        rmsHead      = 0;
        for (int i = 0; i < ITEM_COUNT; i++) {
            barHeights[i] = BAR_MIN_HEIGHT;
            barTargets[i] = BAR_MIN_HEIGHT;
        }

        CVDisplayLinkCreateWithActiveCGDisplays(&displayLink);
        CVDisplayLinkSetOutputCallback(displayLink, displayLinkCallback,
                                       (__bridge void *)self);
        CVDisplayLinkStart(displayLink);
    }
    return self;
}

- (void)dealloc
{
    CVDisplayLinkStop(displayLink);
    CVDisplayLinkRelease(displayLink);
    [super dealloc];
}

- (void)tick:(double)dt
{
    animTime     += dt;
    shimmerPhase += dt;
    for (int i = 0; i < ITEM_COUNT; i++) {
        barHeights[i] = barHeights[i] * 0.7 + barTargets[i] * 0.3;
    }
    [self setNeedsDisplay:YES];
}

- (BOOL)isOpaque { return NO; }
- (BOOL)wantsLayer { return YES; }

- (void)rightMouseDown:(NSEvent *)event
{
    if (!g_context_menu_enabled) return;
    NSMenu *menu = [[NSMenu alloc] initWithTitle:@""];
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

- (void)drawRect:(NSRect)dirtyRect
{
    (void)dirtyRect;

    CGContextRef ctx = [[NSGraphicsContext currentContext] CGContext];
    NSRect bounds = self.bounds;
    double w = bounds.size.width;
    double h = bounds.size.height;
    double r = h / 2.0;  /* radius = half height for true pill */

    /* Clear */
    CGContextClearRect(ctx, bounds);

    /* Pill background.
       Use floor() to ensure 2*r never exceeds h due to IEEE-754 rounding.
       CGPathAddRoundedRect asserts: 2*corner_radius <= side_length. */
    r = floor(MIN(w, h) / 2.0);
    CGMutablePathRef path = CGPathCreateMutable();
    CGPathAddRoundedRect(path, NULL,
                         CGRectMake(0, 0, w, h), r, r);
    CGContextAddPath(ctx, path);
    CGPathRelease(path);
    /* Dark tint over the blur backdrop — lighter than before since the
       NSVisualEffectView beneath provides the frosted-glass body. */
    CGContextSetRGBFillColor(ctx, 0, 0, 0, 0.28);
    CGContextFillPath(ctx);

    /* 1.5 px white border, inset by half the stroke width so it is not
       clipped by the NSVisualEffectView pill mask. */
    {
        CGFloat inset   = 0.75;
        CGFloat borderR = r - inset;
        if (borderR < 0) borderR = 0;
        CGMutablePathRef bp = CGPathCreateMutable();
        CGPathAddRoundedRect(bp, NULL,
            CGRectMake(inset, inset, w - inset * 2, h - inset * 2),
            borderR, borderR);
        CGContextAddPath(ctx, bp);
        CGPathRelease(bp);
        CGContextSetRGBStrokeColor(ctx, 1, 1, 1, 0.25);
        CGContextSetLineWidth(ctx, 1.5);
        CGContextStrokePath(ctx);
    }

    switch (state) {
    case OVERLAY_STATE_IDLE:   [self drawDots:ctx w:w h:h]; break;
    case OVERLAY_STATE_RECORDING:  [self drawBars:ctx w:w h:h]; break;
    case OVERLAY_STATE_TRANSCRIBING: [self drawShimmer:ctx w:w h:h]; break;
    }
}

- (void)drawDots:(CGContextRef)ctx w:(double)w h:(double)h
{
    double spacing  = 10.0, dotR = 3.0;
    double totalW   = (ITEM_COUNT - 1) * spacing;
    double startX   = (w - totalW) / 2.0;
    double cy       = h / 2.0;

    for (int i = 0; i < ITEM_COUNT; i++) {
        double phi = 2.0 * M_PI * animTime / 4.0 + i * 2.0 * M_PI / (double)ITEM_COUNT;
        double s   = sin(phi);
        double a   = 0.35 + 0.65 * s * s;
        double cx  = startX + i * spacing;
        CGContextSetRGBFillColor(ctx, 1, 1, 1, a);
        CGContextFillEllipseInRect(ctx,
            CGRectMake(cx - dotR, cy - dotR, dotR*2, dotR*2));
    }
}

- (void)drawBars:(CGContextRef)ctx w:(double)w h:(double)h
{
    double spacing = 8.0, bw = 5.0, br = 2.5;
    double totalW  = (ITEM_COUNT - 1) * spacing;
    double startX  = (w - totalW) / 2.0;
    double cy      = h / 2.0;

    CGContextSetRGBFillColor(ctx, 1, 1, 1, 1);
    for (int i = 0; i < ITEM_COUNT; i++) {
        double bh = barHeights[i];
        double cx = startX + i * spacing;
        double x  = cx - bw / 2.0;
        double y  = cy - bh / 2.0;
        CGRect  rect = CGRectMake(x, y, bw, bh);
        /* Clamp radius so 2*ri <= MIN(bw, bh) — CGPath asserts otherwise. */
        CGFloat ri = (CGFloat)MIN(br, MIN(bw, bh) / 2.0);
        CGMutablePathRef p = CGPathCreateMutable();
        CGPathAddRoundedRect(p, NULL, rect, ri, ri);
        CGContextAddPath(ctx, p);
        CGPathRelease(p);
        CGContextFillPath(ctx);
    }
}

- (void)drawShimmer:(CGContextRef)ctx w:(double)w h:(double)h
{
    NSDictionary *attrs = @{
        NSFontAttributeName: [NSFont systemFontOfSize:14
                                               weight:NSFontWeightMedium],
        NSForegroundColorAttributeName: [NSColor whiteColor]
    };
    NSString *text = @"transcribing";
    NSSize sz  = [text sizeWithAttributes:attrs];
    NSPoint pt = NSMakePoint(floor((w - sz.width)  / 2.0),
                             floor((h - sz.height) / 2.0));

    /* Transparency layer so SourceIn clips gradient strictly to text ink. */
    CGContextBeginTransparencyLayer(ctx, NULL);

    /* Base text — dim, so the sweep contrast is visible. */
    NSDictionary *dimAttrs = @{
        NSFontAttributeName: [NSFont systemFontOfSize:14
                                               weight:NSFontWeightMedium],
        NSForegroundColorAttributeName: [NSColor colorWithWhite:1 alpha:0.28]
    };
    [text drawAtPoint:pt withAttributes:dimAttrs];

    /* Wide soft band sweeping left → right over 2 seconds. */
    CGContextSaveGState(ctx);
    CGContextSetBlendMode(ctx, kCGBlendModeSourceIn);

    double bandW  = sz.width * 0.85;               /* band ≈ 85 % of text width  */
    double travel = sz.width + bandW;              /* enter fully, exit fully     */
    double phase  = fmod(shimmerPhase / 2.0, 1.0); /* 2-second cycle             */
    double cx     = pt.x - bandW * 0.5 + travel * phase;

    /* Gradient stops: flat-zero → gentle rise → bright peak → gentle fall → flat-zero.
       Keeping the bright zone narrow at the centre gives the "light gleam" feel. */
    NSGradient *grad = [[NSGradient alloc]
        initWithColors:@[
            [NSColor colorWithWhite:1 alpha:0.0],
            [NSColor colorWithWhite:1 alpha:0.0],
            [NSColor colorWithWhite:1 alpha:0.9],
            [NSColor colorWithWhite:1 alpha:0.0],
            [NSColor colorWithWhite:1 alpha:0.0]
        ]
        atLocations:(CGFloat[]){0.0, 0.2, 0.5, 0.8, 1.0}
        colorSpace:[NSColorSpace genericRGBColorSpace]];

    [grad drawInRect:NSMakeRect(cx - bandW * 0.5, pt.y, bandW, sz.height) angle:0];

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

static SussurroPanel *g_panel = nil;
static SussurroView  *g_view  = nil;

void* overlay_create_macos(void)
{
    NSScreen *screen = [NSScreen mainScreen];
    NSRect    sf     = screen.frame;
    NSRect    frame  = NSMakeRect(
        (sf.size.width  - 220) / 2.0,
        24,
        220, 52);

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
    CGFloat blurR   = floor(MIN(frame.size.width, frame.size.height) / 2.0);

    /* NSVisualEffectView — real OS-level blur of whatever is behind the window. */
    NSVisualEffectView *blurView = [[NSVisualEffectView alloc]
                                    initWithFrame:viewRect];
    blurView.material     = NSVisualEffectMaterialHUDWindow;
    blurView.blendingMode = NSVisualEffectBlendingModeBehindWindow;
    blurView.state        = NSVisualEffectStateActive;
    blurView.appearance   = [NSAppearance
                              appearanceNamed:NSAppearanceNameVibrantDark];
    blurView.wantsLayer   = YES;

    /* Clip the blur strictly to the pill silhouette so it doesn't bleed
       outside the capsule on either axis. */
    CAShapeLayer *pillMask = [CAShapeLayer layer];
    pillMask.path = CGPathCreateWithRoundedRect(
        CGRectMake(0, 0, frame.size.width, frame.size.height),
        blurR, blurR, NULL);
    blurView.layer.mask = pillMask;

    g_view = [[SussurroView alloc] initWithFrame:viewRect];
    [blurView addSubview:g_view];
    [g_panel setContentView:blurView];

    /* Deliberately not shown here. The panel is ordered front only while
       something is happening (see overlay_show_macos), so an idle Sussurro
       leaves nothing on screen. */

    return (__bridge void *)g_panel;
}

void overlay_set_state_macos(int state)
{
    dispatch_async(dispatch_get_main_queue(), ^{
        if (g_view) g_view->state = state;
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

void overlay_show_macos(void)
{
    dispatch_async(dispatch_get_main_queue(), ^{
        [g_panel orderFrontRegardless];
    });
}

void overlay_hide_macos(void)
{
    dispatch_async(dispatch_get_main_queue(), ^{
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
        CVDisplayLinkStop(g_view->displayLink);
    }
    if (g_panel) {
        [g_panel orderOut:nil];
    }
    /* _exit() skips atexit/C++ destructors, preventing the Metal assertion. */
    _exit(0);
}
