/* Win32 implementation of the Sussurro capsule overlay.
 *
 * A layered (per-pixel alpha) topmost tool window drawn with the GDI+ flat C
 * API into a premultiplied-ARGB DIB section, pushed to the screen with
 * UpdateLayeredWindow each animation tick. The drawing math (pill, dots,
 * bars, shimmer) is a 1:1 port of overlay_linux.c's Cairo code.
 *
 * Threading: the window is created on the caller's thread (the main thread,
 * which later runs the webview GetMessage loop — that loop dispatches for
 * every window owned by the thread, including this one). Cross-thread state
 * and RMS updates arrive via PostMessage.
 */

#include <windows.h>
#include <objidl.h>
#include <math.h>
#include <string.h>
#include <gdiplus.h>

#include "overlay_windows.h"

#ifndef M_PI
#define M_PI 3.14159265358979323846
#endif

#define WM_APP_SET_STATE (WM_APP + 1)
#define WM_APP_PUSH_RMS  (WM_APP + 2)

#define MENU_ID_SETTINGS 1
#define MENU_ID_QUIT     2

typedef struct OverlayData {
    HWND        hwnd;

    int         state;        /* OVERLAY_STATE_* */
    double      anim_time;    /* seconds, incremented each tick */

    float       rms_ring[ITEM_COUNT];
    int         rms_head;
    double      bar_heights[ITEM_COUNT];
    double      bar_targets[ITEM_COUNT];

    double      shimmer_phase;

    /* Render resources (created once) */
    HDC         mem_dc;
    HBITMAP     dib;
    void       *dib_bits;
    GpBitmap   *gp_bitmap;    /* wraps dib_bits as 32bpp PARGB */
    GpGraphics *gfx;

    int         pos_x, pos_y;

    MenuOpenSettingsCB open_settings_cb;
    MenuQuitCB         quit_cb;
} OverlayData;

static ULONG_PTR g_gdiplus_token = 0;

/* ------------------------------------------------------------------ */
/* Drawing helpers                                                     */
/* ------------------------------------------------------------------ */

/* Rounded-rect path: arcs at the four corners (angles in degrees). */
static void pill_path(GpPath *path, float x, float y, float w, float h, float r)
{
    GdipResetPath(path);
    GdipAddPathArc(path, x,             y,             2*r, 2*r, 180.0f, 90.0f);
    GdipAddPathArc(path, x + w - 2*r,   y,             2*r, 2*r, 270.0f, 90.0f);
    GdipAddPathArc(path, x + w - 2*r,   y + h - 2*r,   2*r, 2*r,   0.0f, 90.0f);
    GdipAddPathArc(path, x,             y + h - 2*r,   2*r, 2*r,  90.0f, 90.0f);
    GdipClosePathFigure(path);
}

static void draw_idle_dots(OverlayData *od)
{
    float total_w  = (ITEM_COUNT - 1) * DOT_SPACING;
    float start_x  = (OVERLAY_WIDTH - total_w) / 2.0f;
    float center_y = OVERLAY_HEIGHT / 2.0f;

    for (int i = 0; i < ITEM_COUNT; i++) {
        double t   = od->anim_time;
        double phi = 2.0 * M_PI * t / 4.0 + i * 2.0 * M_PI / (double)ITEM_COUNT;
        double s   = sin(phi);
        double a   = 0.35 + 0.65 * s * s;

        ARGB color = ((ARGB)(a * 255.0) << 24) | 0x00FFFFFFu;
        GpSolidFill *brush = NULL;
        GdipCreateSolidFill(color, &brush);
        float cx = start_x + i * DOT_SPACING;
        GdipFillEllipse(od->gfx, (GpBrush *)brush,
                        cx - DOT_RADIUS, center_y - DOT_RADIUS,
                        2 * DOT_RADIUS, 2 * DOT_RADIUS);
        GdipDeleteBrush((GpBrush *)brush);
    }
}

static void draw_recording_bars(OverlayData *od)
{
    float total_w  = (ITEM_COUNT - 1) * BAR_SPACING;
    float start_x  = (OVERLAY_WIDTH - total_w) / 2.0f;
    float center_y = OVERLAY_HEIGHT / 2.0f;

    GpSolidFill *brush = NULL;
    GdipCreateSolidFill(0xFFFFFFFFu, &brush);

    GpPath *path = NULL;
    GdipCreatePath(FillModeAlternate, &path);

    for (int i = 0; i < ITEM_COUNT; i++) {
        float h  = (float)od->bar_heights[i];
        float cx = start_x + i * BAR_SPACING;
        float x  = cx - BAR_WIDTH / 2.0f;
        float y  = center_y - h / 2.0f;

        float r = BAR_RADIUS;
        if (r > h / 2.0f) r = h / 2.0f;

        pill_path(path, x, y, BAR_WIDTH, h, r);
        GdipFillPath(od->gfx, (GpBrush *)brush, path);
    }

    GdipDeletePath(path);
    GdipDeleteBrush((GpBrush *)brush);
}

static void draw_transcribing_text(OverlayData *od)
{
    /* Cleanup shares this shimmer but not its label: no recognition runs
     * during it, and on the partial-reuse path none ran at all. */
    const WCHAR *text = (od->state == OVERLAY_STATE_CLEANING_UP)
        ? L"cleaning up" : L"transcribing";

    GpFontFamily *family = NULL;
    if (GdipCreateFontFamilyFromName(L"Segoe UI", NULL, &family) != Ok || !family)
        return;
    GpFont *font = NULL;
    GdipCreateFont(family, 14.0f, FontStyleRegular, UnitPixel, &font);

    GpStringFormat *fmt = NULL;
    GdipCreateStringFormat(0, 0, &fmt);
    GdipSetStringFormatAlign(fmt, StringAlignmentCenter);
    GdipSetStringFormatLineAlign(fmt, StringAlignmentCenter);

    RectF layout = { 0.0f, 0.0f, (float)OVERLAY_WIDTH, (float)OVERLAY_HEIGHT };

    /* Base white text @ 0.7 alpha */
    GpSolidFill *base = NULL;
    GdipCreateSolidFill(0xB3FFFFFFu, &base); /* 0.7 * 255 = 179 = 0xB3 */
    GdipDrawString(od->gfx, text, -1, font, &layout, fmt, (GpBrush *)base);
    GdipDeleteBrush((GpBrush *)base);

    /* Shimmer: white highlight sweeping left-to-right over 1.5 s. */
    RectF bounds = { 0 };
    GdipMeasureString(od->gfx, text, -1, font, &layout, fmt, &bounds, NULL, NULL);

    double phase     = fmod(od->shimmer_phase, 1.5) / 1.5; /* 0..1 */
    float  shimmer_x = bounds.X - 40.0f + (bounds.Width + 80.0f) * (float)phase;

    RectF grad_rect = { shimmer_x - 20.0f, 0.0f, 40.0f, (float)OVERLAY_HEIGHT };
    GpLineGradient *grad = NULL;
    if (GdipCreateLineBrushFromRect(&grad_rect, 0x00FFFFFFu, 0x00FFFFFFu,
                                    LinearGradientModeHorizontal,
                                    WrapModeTileFlipX, &grad) == Ok && grad) {
        ARGB colors[3]    = { 0x00FFFFFFu, 0x80FFFFFFu, 0x00FFFFFFu };
        REAL positions[3] = { 0.0f, 0.5f, 1.0f };
        GdipSetLinePresetBlend(grad, colors, positions, 3);

        GpPath *clip = NULL;
        GdipCreatePath(FillModeAlternate, &clip);
        pill_path(clip, 0, 0, OVERLAY_WIDTH, OVERLAY_HEIGHT, OVERLAY_RADIUS);
        GdipSetClipPath(od->gfx, clip, CombineModeReplace);

        GdipDrawString(od->gfx, text, -1, font, &layout, fmt, (GpBrush *)grad);

        GdipResetClip(od->gfx);
        GdipDeletePath(clip);
        GdipDeleteBrush((GpBrush *)grad);
    }

    GdipDeleteStringFormat(fmt);
    GdipDeleteFont(font);
    GdipDeleteFontFamily(family);
}

static void render_frame(OverlayData *od)
{
    GdipGraphicsClear(od->gfx, 0x00000000u);

    GpPath *path = NULL;
    GdipCreatePath(FillModeAlternate, &path);
    pill_path(path, 0, 0, OVERLAY_WIDTH, OVERLAY_HEIGHT, OVERLAY_RADIUS);

    /* Background fill + subtle white rim */
    GpSolidFill *bg = NULL;
    GdipCreateSolidFill(BG_ARGB, &bg);
    GdipFillPath(od->gfx, (GpBrush *)bg, path);
    GdipDeleteBrush((GpBrush *)bg);

    GpPen *rim = NULL;
    GdipCreatePen1(BORDER_ARGB, 1.5f, UnitPixel, &rim);
    GdipDrawPath(od->gfx, rim, path);
    GdipDeletePen(rim);
    GdipDeletePath(path);

    switch (od->state) {
    case OVERLAY_STATE_IDLE:         draw_idle_dots(od);         break;
    case OVERLAY_STATE_RECORDING:    draw_recording_bars(od);    break;
    case OVERLAY_STATE_TRANSCRIBING:
    case OVERLAY_STATE_CLEANING_UP:  draw_transcribing_text(od); break;
    }

    GdipFlush(od->gfx, FlushIntentionSync);

    /* Push the premultiplied-ARGB surface to the screen. */
    HDC screen = GetDC(NULL);
    POINT pt_src = { 0, 0 };
    POINT pt_pos = { od->pos_x, od->pos_y };
    SIZE  size   = { OVERLAY_WIDTH, OVERLAY_HEIGHT };
    BLENDFUNCTION blend = { AC_SRC_OVER, 0, 255, AC_SRC_ALPHA };
    UpdateLayeredWindow(od->hwnd, screen, &pt_pos, &size,
                        od->mem_dc, &pt_src, 0, &blend, ULW_ALPHA);
    ReleaseDC(NULL, screen);
}

/* ------------------------------------------------------------------ */
/* Window procedure                                                    */
/* ------------------------------------------------------------------ */

static void show_context_menu(OverlayData *od)
{
    HMENU menu = CreatePopupMenu();
    AppendMenuW(menu, MF_STRING, MENU_ID_SETTINGS, L"Open Settings");
    AppendMenuW(menu, MF_SEPARATOR, 0, NULL);
    AppendMenuW(menu, MF_STRING, MENU_ID_QUIT, L"Quit");

    POINT pt;
    GetCursorPos(&pt);
    /* Required for a WS_EX_NOACTIVATE window so the menu dismisses when the
     * user clicks elsewhere. */
    SetForegroundWindow(od->hwnd);
    int cmd = TrackPopupMenuEx(menu,
                               TPM_RETURNCMD | TPM_RIGHTBUTTON | TPM_NONOTIFY,
                               pt.x, pt.y, od->hwnd, NULL);
    DestroyMenu(menu);

    if (cmd == MENU_ID_SETTINGS && od->open_settings_cb) od->open_settings_cb();
    else if (cmd == MENU_ID_QUIT && od->quit_cb)         od->quit_cb();
}

static LRESULT CALLBACK overlay_wndproc(HWND hwnd, UINT msg, WPARAM wparam, LPARAM lparam)
{
    OverlayData *od = (OverlayData *)GetWindowLongPtrW(hwnd, GWLP_USERDATA);

    switch (msg) {
    case WM_TIMER:
        if (od) {
            double dt = 1.0 / 60.0;
            od->anim_time     += dt;
            od->shimmer_phase += dt;
            for (int i = 0; i < ITEM_COUNT; i++)
                od->bar_heights[i] = od->bar_heights[i] * 0.7 + od->bar_targets[i] * 0.3;
            render_frame(od);
        }
        return 0;

    case WM_APP_SET_STATE:
        if (od) od->state = (int)wparam;
        return 0;

    case WM_APP_PUSH_RMS:
        if (od) {
            union { UINT32 u; float f; } cvt;
            cvt.u = (UINT32)wparam;
            od->rms_ring[od->rms_head] = cvt.f;
            od->rms_head = (od->rms_head + 1) % ITEM_COUNT;
            for (int i = 0; i < ITEM_COUNT; i++) {
                int idx = (od->rms_head + i) % ITEM_COUNT;
                double norm = od->rms_ring[idx] / RMS_SCALE;
                if (norm > 1.0) norm = 1.0;
                od->bar_targets[i] = BAR_MIN_HEIGHT + norm * (BAR_MAX_HEIGHT - BAR_MIN_HEIGHT);
            }
        }
        return 0;

    case WM_RBUTTONUP:
        if (od && (od->open_settings_cb || od->quit_cb)) show_context_menu(od);
        return 0;

    case WM_MOUSEACTIVATE:
        return MA_NOACTIVATE;   /* never steal keyboard focus from the target app */

    case WM_CLOSE:
        ShowWindow(hwnd, SW_HIDE); /* the overlay is never destroyed */
        return 0;

    case WM_DESTROY:
        KillTimer(hwnd, 1);
        return 0;
    }
    return DefWindowProcW(hwnd, msg, wparam, lparam);
}

/* ------------------------------------------------------------------ */
/* Public API                                                          */
/* ------------------------------------------------------------------ */

void *overlay_create(void)
{
    /* Per-monitor-v2 DPI awareness, set before any window exists in the
     * process (overlay_create is the first window-creating call in
     * Manager.Run). Fails harmlessly if awareness was already fixed. */
    typedef BOOL (WINAPI *SetDpiCtxFn)(HANDLE);
    HMODULE user32 = GetModuleHandleW(L"user32.dll");
    if (user32) {
        SetDpiCtxFn set_ctx = (SetDpiCtxFn)(void *)GetProcAddress(user32, "SetProcessDpiAwarenessContext");
        if (set_ctx) set_ctx(/* DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 */ (HANDLE)-4);
    }

    if (g_gdiplus_token == 0) {
        GdiplusStartupInput input;
        memset(&input, 0, sizeof(input));
        input.GdiplusVersion = 1;
        GdiplusStartup(&g_gdiplus_token, &input, NULL);
    }

    HINSTANCE hinst = GetModuleHandleW(NULL);

    static int class_registered = 0;
    if (!class_registered) {
        WNDCLASSEXW wc;
        memset(&wc, 0, sizeof(wc));
        wc.cbSize        = sizeof(wc);
        wc.lpfnWndProc   = overlay_wndproc;
        wc.hInstance     = hinst;
        wc.hCursor       = LoadCursorW(NULL, (LPCWSTR)IDC_ARROW);
        wc.lpszClassName = L"SussurroOverlay";
        RegisterClassExW(&wc);
        class_registered = 1;
    }

    /* Bottom-center of the primary monitor's work area, 24 px up: on Windows
     * the taskbar occupies the full-monitor bottom strip, so the work area is
     * the equivalent of the Linux overlay's screen-bottom placement. */
    RECT work = { 0, 0, 1920, 1080 };
    SystemParametersInfoW(SPI_GETWORKAREA, 0, &work, 0);
    int x = work.left + ((work.right - work.left) - OVERLAY_WIDTH) / 2;
    int y = work.bottom - OVERLAY_HEIGHT - OVERLAY_MARGIN_BOTTOM;

    HWND hwnd = CreateWindowExW(
        WS_EX_LAYERED | WS_EX_TOPMOST | WS_EX_TOOLWINDOW | WS_EX_NOACTIVATE,
        L"SussurroOverlay", L"Sussurro Overlay",
        WS_POPUP,
        x, y, OVERLAY_WIDTH, OVERLAY_HEIGHT,
        NULL, NULL, hinst, NULL);
    if (!hwnd) return NULL;

    OverlayData *od = (OverlayData *)calloc(1, sizeof(OverlayData));
    od->hwnd  = hwnd;
    od->state = OVERLAY_STATE_IDLE;
    od->pos_x = x;
    od->pos_y = y;
    for (int i = 0; i < ITEM_COUNT; i++) {
        od->bar_heights[i] = BAR_MIN_HEIGHT;
        od->bar_targets[i] = BAR_MIN_HEIGHT;
    }

    /* Top-down 32bpp DIB shared between GDI (UpdateLayeredWindow) and GDI+
     * (drawing, via the PARGB Scan0 wrapper). */
    HDC screen = GetDC(NULL);
    od->mem_dc = CreateCompatibleDC(screen);
    ReleaseDC(NULL, screen);

    BITMAPINFO bmi;
    memset(&bmi, 0, sizeof(bmi));
    bmi.bmiHeader.biSize        = sizeof(BITMAPINFOHEADER);
    bmi.bmiHeader.biWidth       = OVERLAY_WIDTH;
    bmi.bmiHeader.biHeight      = -OVERLAY_HEIGHT; /* top-down */
    bmi.bmiHeader.biPlanes      = 1;
    bmi.bmiHeader.biBitCount    = 32;
    bmi.bmiHeader.biCompression = BI_RGB;
    od->dib = CreateDIBSection(od->mem_dc, &bmi, DIB_RGB_COLORS, &od->dib_bits, NULL, 0);
    SelectObject(od->mem_dc, od->dib);

    GdipCreateBitmapFromScan0(OVERLAY_WIDTH, OVERLAY_HEIGHT, OVERLAY_WIDTH * 4,
                              PixelFormat32bppPARGB, (BYTE *)od->dib_bits, &od->gp_bitmap);
    GdipGetImageGraphicsContext((GpImage *)od->gp_bitmap, &od->gfx);
    GdipSetSmoothingMode(od->gfx, SmoothingModeAntiAlias);
    GdipSetTextRenderingHint(od->gfx, TextRenderingHintAntiAlias);

    SetWindowLongPtrW(hwnd, GWLP_USERDATA, (LONG_PTR)od);

    render_frame(od); /* first frame before showing, to avoid a blank flash */

    /* Deliberately not shown here, and no timer started. The overlay is
       mapped only while something is happening (see overlay_show), so an idle
       Sussurro leaves nothing on screen and burns no redraws. */

    return (void *)hwnd;
}

void overlay_show(void *hwnd)
{
    /* ~60 fps, same cadence as g_timeout_add(16). Started on show and killed
       on hide so a hidden overlay costs nothing. */
    SetTimer((HWND)hwnd, 1, 16, NULL);
    ShowWindow((HWND)hwnd, SW_SHOWNOACTIVATE);
}

void overlay_hide(void *hwnd)
{
    KillTimer((HWND)hwnd, 1);
    ShowWindow((HWND)hwnd, SW_HIDE);
}

void overlay_set_state_async(void *hwnd, int state)
{
    PostMessageW((HWND)hwnd, WM_APP_SET_STATE, (WPARAM)state, 0);
}

void overlay_push_rms_async(void *hwnd, float rms)
{
    union { UINT32 u; float f; } cvt;
    cvt.f = rms;
    PostMessageW((HWND)hwnd, WM_APP_PUSH_RMS, (WPARAM)cvt.u, 0);
}

void overlay_install_context_menu(void *hwnd,
                                  MenuOpenSettingsCB open_settings_cb,
                                  MenuQuitCB quit_cb)
{
    OverlayData *od = (OverlayData *)GetWindowLongPtrW((HWND)hwnd, GWLP_USERDATA);
    if (!od) return;
    od->open_settings_cb = open_settings_cb;
    od->quit_cb          = quit_cb;
}
