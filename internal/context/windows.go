//go:build windows

package context

import (
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WindowsProvider implements context detection for Windows using Win32 APIs.
type WindowsProvider struct{}

// NewWindowsProvider creates a new Windows context provider
func NewWindowsProvider() *WindowsProvider {
	return &WindowsProvider{}
}

var (
	ctxUser32                    = windows.NewLazySystemDLL("user32.dll")
	procGetForegroundWindow      = ctxUser32.NewProc("GetForegroundWindow")
	procGetWindowTextW           = ctxUser32.NewProc("GetWindowTextW")
	procGetWindowThreadProcessId = ctxUser32.NewProc("GetWindowThreadProcessId")
)

// GetContext retrieves the current application name and window title.
// Like the other platforms it never returns an error; unavailable fields
// (lock screen, elevated foreground process) fall back to "unknown".
func (p *WindowsProvider) GetContext() (*ContextInfo, error) {
	info := &ContextInfo{
		AppName:     "unknown",
		WindowTitle: "unknown",
		Timestamp:   time.Now(),
	}

	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return info, nil
	}

	var title [512]uint16
	if n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&title[0])), uintptr(len(title))); n > 0 {
		info.WindowTitle = windows.UTF16ToString(title[:n])
	}

	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid != 0 {
		if h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid); err == nil {
			var path [windows.MAX_PATH]uint16
			size := uint32(len(path))
			if err := windows.QueryFullProcessImageName(h, 0, &path[0], &size); err == nil {
				exe := filepath.Base(windows.UTF16ToString(path[:size]))
				if strings.EqualFold(filepath.Ext(exe), ".exe") {
					exe = exe[:len(exe)-len(".exe")]
				}
				info.AppName = exe
			}
			windows.CloseHandle(h)
		}
	}

	return info, nil
}

// Close cleans up any resources used by the provider
func (p *WindowsProvider) Close() error {
	return nil
}
