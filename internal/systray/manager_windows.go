//go:build windows

// Package systray manages the Windows system tray icon and menu for WiNotification.
// Author: Hadi Cahyadi <cumulus13@gmail.com>
package systray

import (
	"os"
	"sync/atomic"
	"unsafe"
	"context"

	"github.com/getlantern/systray"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/windows"
)

// State tracks whether capture is running or paused.
type State int32

const (
	StateRunning State = iota
	StatePaused
	StateStopped
)

// Manager controls the system tray icon lifecycle.
type Manager struct {
	iconPath string
	log      *logrus.Logger

	state    atomic.Int32
	pauseCh  chan struct{} // pause/resume toggle
	stopCh   chan struct{} // stop/start toggle
	reloadCh chan struct{} // reload config signal
	quitCh   chan struct{} // quit signal (internal)

	cancelFn context.CancelFunc
}

// NewManager creates a Manager. cancelFn is called when the user clicks Quit.
func NewManager(iconPath string, log *logrus.Logger, cancelFn context.CancelFunc) *Manager {
	m := &Manager{
		iconPath: iconPath,
		log:      log,
		pauseCh:  make(chan struct{}, 1),
		stopCh:   make(chan struct{}, 1),
		reloadCh: make(chan struct{}, 1),
		quitCh:   make(chan struct{}, 1),
		cancelFn: cancelFn,
	}
	m.state.Store(int32(StateRunning))
	return m
}

// Run starts the systray event loop (blocks until Quit).
func (m *Manager) Run() {
	systray.Run(m.onReady, m.onExit)
}

// PauseCh returns the channel that signals a pause/resume toggle.
func (m *Manager) PauseCh() <-chan struct{} { return m.pauseCh }

// StopCh returns the channel that signals capture stop/start.
func (m *Manager) StopCh() <-chan struct{} { return m.stopCh }

// ReloadCh returns the channel that signals a config reload.
func (m *Manager) ReloadCh() <-chan struct{} { return m.reloadCh }

// IsPaused reports whether capture is currently paused.
func (m *Manager) IsPaused() bool {
	return State(m.state.Load()) == StatePaused
}

func (m *Manager) onReady() {
	// Load tray icon from file — fall back to minimal blank ICO if missing.
	// Note: getlantern/systray only supports a real ICO/PNG for the tray icon
	// itself. Individual menu items do NOT support bitmap icons on Windows via
	// this library. We therefore prefix every menu label with a Unicode emoji
	// that renders fine on Windows 10/11 (Segoe UI Emoji font is always present).
	// This is the best practical approach without dropping into raw Win32 APIs.
	iconData, err := os.ReadFile(m.iconPath)
	if err != nil {
		m.log.Warnf("[systray] icon not found at %s — using built-in fallback (🔔)", m.iconPath)
		iconData = defaultIcon()
	}

	systray.SetIcon(iconData)
	systray.SetTitle("WiNotification")
	systray.SetTooltip("🔔 WiNotification — Windows notification forwarder\nRight-click for options")

	// ── Status label (disabled, top of menu) ─────────────────
	// Emoji: 🟢 = running  ⏸ = paused  ⏹ = stopped
	mStatus := systray.AddMenuItem("🟢 Running", "Current capture status")
	mStatus.Disable()

	systray.AddSeparator()

	// ── Capture controls ──────────────────────────────────────
	mPause  := systray.AddMenuItem("⏸  Pause",  "Temporarily suspend notification forwarding")
	mResume := systray.AddMenuItem("▶️  Resume", "Resume notification forwarding")
	mResume.Hide()

	mStop  := systray.AddMenuItem("⏹  Stop",  "Stop capture loop (app stays in tray)")
	mStart := systray.AddMenuItem("🚀 Start", "Restart the capture loop")
	mStart.Hide()

	systray.AddSeparator()

	// ── Reload config ─────────────────────────────────────────
	mReload := systray.AddMenuItem("🔄 Reload Config", "Reload config.toml without restarting")

	systray.AddSeparator()

	// ── Info ──────────────────────────────────────────────────
	mAbout := systray.AddMenuItem("ℹ️  About WiNotification", "Version & author info")

	systray.AddSeparator()

	// ── Exit ──────────────────────────────────────────────────
	mQuit := systray.AddMenuItem("❌ Quit", "Exit WiNotification completely")

	// ── Event loop ────────────────────────────────────────────
	go func() {
		for {
			select {

			case <-mPause.ClickedCh:
				m.state.Store(int32(StatePaused))
				mStatus.SetTitle("⏸ Paused")
				mPause.Hide()
				mResume.Show()
				m.pauseCh <- struct{}{}
				m.log.Info("[systray] capture paused")

			case <-mResume.ClickedCh:
				m.state.Store(int32(StateRunning))
				mStatus.SetTitle("🟢 Running")
				mResume.Hide()
				mPause.Show()
				m.pauseCh <- struct{}{}
				m.log.Info("[systray] capture resumed")

			case <-mStop.ClickedCh:
				m.state.Store(int32(StateStopped))
				mStatus.SetTitle("⏹ Stopped")
				mStop.Hide()
				mStart.Show()
				mPause.Hide()
				mResume.Hide()
				m.stopCh <- struct{}{}
				m.log.Info("[systray] capture stopped")

			case <-mStart.ClickedCh:
				m.state.Store(int32(StateRunning))
				mStatus.SetTitle("🟢 Running")
				mStart.Hide()
				mStop.Show()
				mPause.Show()
				m.stopCh <- struct{}{}
				m.log.Info("[systray] capture started")

			case <-mReload.ClickedCh:
				m.log.Info("[systray] reload config requested")
				m.reloadCh <- struct{}{}

			case <-mAbout.ClickedCh:
				m.log.Info("[systray] about clicked")
				showAboutBox()

			case <-mQuit.ClickedCh:
				m.log.Info("[systray] quit requested")
				m.cancelFn()
				systray.Quit()
				return
			}
		}
	}()
}

func (m *Manager) onExit() {
	m.log.Info("[systray] exiting")
}

// showAboutBox displays a native Windows MessageBox with version/author info.
// This avoids any dependency on the toast forwarder from inside the systray package.
func showAboutBox() {
	go func() {
		user32 := windows.NewLazySystemDLL("user32.dll")
		msgBox := user32.NewProc("MessageBoxW")

		title, _ := windows.UTF16PtrFromString("About WiNotification")
		text, _ := windows.UTF16PtrFromString(
			"WiNotification v1.0.0\n" +
			"─────────────────────────\n" +
			"Windows notification forwarder\n\n" +
			"Author:   Hadi Cahyadi\n" +
			"Email:    cumulus13@gmail.com\n" +
			"Homepage: github.com/cumulus13/WiNotification\n\n" +
			"Backends: Growl · ntfy · RabbitMQ · ZeroMQ\n" +
			"          Redis · Database · Toast",
		)
		// MB_OK | MB_ICONINFORMATION = 0x40
		msgBox.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x40)
	}()
}

// defaultIcon returns a minimal valid ICO (1×1 transparent 32bpp) used when
// no icon file is found. The tray icon will appear blank; users should drop
// icons/icon.ico into the app folder.
//
// Fallback tooltip label: 🔔 (set via SetTooltip — the tray icon itself is
// binary ICO data and cannot be an emoji).
func defaultIcon() []byte {
	// Minimal valid Windows ICO: 1×1 pixel, 32bpp, fully transparent.
	return []byte{
		// ICO header
		0x00, 0x00, // reserved
		0x01, 0x00, // type: ICO
		0x01, 0x00, // image count: 1
		// Directory entry
		0x01,       // width: 1
		0x01,       // height: 1
		0x00,       // color count: 0 (true color)
		0x00,       // reserved
		0x01, 0x00, // planes: 1
		0x20, 0x00, // bit count: 32
		0x28, 0x00, 0x00, 0x00, // size of image data
		0x16, 0x00, 0x00, 0x00, // offset of image data
		// BITMAPINFOHEADER (40 bytes)
		0x28, 0x00, 0x00, 0x00, // biSize: 40
		0x01, 0x00, 0x00, 0x00, // biWidth: 1
		0x02, 0x00, 0x00, 0x00, // biHeight: 2 (XOR+AND mask)
		0x01, 0x00,             // biPlanes: 1
		0x20, 0x00,             // biBitCount: 32
		0x00, 0x00, 0x00, 0x00, // biCompression: none
		0x00, 0x00, 0x00, 0x00, // biSizeImage: 0
		0x00, 0x00, 0x00, 0x00, // biXPelsPerMeter
		0x00, 0x00, 0x00, 0x00, // biYPelsPerMeter
		0x00, 0x00, 0x00, 0x00, // biClrUsed
		0x00, 0x00, 0x00, 0x00, // biClrImportant
		// XOR mask: 1 pixel BGRA (fully transparent)
		0x00, 0x00, 0x00, 0x00,
		// AND mask: 1 pixel (padded to DWORD)
		0x00, 0x00, 0x00, 0x00,
	}
}
