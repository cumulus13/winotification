//go:build windows

// Package capture intercepts Windows notifications using two complementary methods:
//
//  1. SetWinEventHook (EVENT_SYSTEM_ALERT) — fires the moment a toast popup
//     appears. Works for ALL apps, no package identity required.
//
//  2. Action Center poll via EnumChildWindows — reads queued notifications
//     from the Action Center window so existing notifications are also captured.
//
// Why NOT UserNotificationListener:
//   Windows.UI.Notifications.Management.UserNotificationListener silently
//   returns zero results from a plain Win32 exe even when AccessStatus=Allowed.
//   It only works from a packaged UWP/MSIX process with a registered AppUserModelId.
//
// Author:   Hadi Cahyadi <cumulus13@gmail.com>
// Homepage: github.com/cumulus13/WiNotification
package capture

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/windows"
)

// ── Win32 constants ───────────────────────────────────────────────────────────

const (
	WINEVENT_OUTOFCONTEXT  = 0x0000
	WINEVENT_SKIPOWNTHREAD = 0x0001

	EVENT_SYSTEM_ALERT  = 0x0002
	EVENT_OBJECT_CREATE = 0x8000

	WM_QUIT = 0x0012
)

// ── DLL + proc references ─────────────────────────────────────────────────────

var (
	modUser32   = windows.NewLazySystemDLL("user32.dll")
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")

	// user32.dll — UI / message / hook functions
	procSetWinEventHook    = modUser32.NewProc("SetWinEventHook")
	procUnhookWinEvent     = modUser32.NewProc("UnhookWinEvent")
	procGetMessageW        = modUser32.NewProc("GetMessageW")
	procTranslateMessage   = modUser32.NewProc("TranslateMessage")
	procDispatchMessageW   = modUser32.NewProc("DispatchMessageW")
	procPostThreadMessageW = modUser32.NewProc("PostThreadMessageW")
	procFindWindowW        = modUser32.NewProc("FindWindowW")
	procGetWindowTextW     = modUser32.NewProc("GetWindowTextW")
	procGetClassNameW      = modUser32.NewProc("GetClassNameW")
	procEnumChildWindows   = modUser32.NewProc("EnumChildWindows")
	procIsWindowVisible    = modUser32.NewProc("IsWindowVisible")

	// kernel32.dll — thread / process functions
	procGetCurrentThreadId = modKernel32.NewProc("GetCurrentThreadId")
)

// MSG mirrors the Win32 MSG structure.
type msgStruct struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	PtX     int32
	PtY     int32
}

// ── Engine ────────────────────────────────────────────────────────────────────

// Engine captures Windows toast notifications via WinEvent hooks.
// It must be Run() which internally locks to an OS thread for hook affinity.
type Engine struct {
	intervalMs int
	filterApps map[string]struct{}
	ignoreApps map[string]struct{}
	logger     *logrus.Logger
	out        chan<- *Notification

	mu   sync.Mutex
	seen map[string]time.Time // dedup: content-key → last seen time
}

func NewEngine(
	intervalMs int,
	filterApps []string,
	ignoreApps []string,
	out chan<- *Notification,
	log *logrus.Logger,
) *Engine {
	fa := make(map[string]struct{}, len(filterApps))
	for _, a := range filterApps {
		fa[strings.ToLower(a)] = struct{}{}
	}
	ia := make(map[string]struct{}, len(ignoreApps))
	for _, a := range ignoreApps {
		ia[strings.ToLower(a)] = struct{}{}
	}
	return &Engine{
		intervalMs: intervalMs,
		filterApps: fa,
		ignoreApps: ia,
		logger:     log,
		out:        out,
		seen:       make(map[string]time.Time),
	}
}

// Run locks an OS thread, installs WinEvent hooks, and pumps Win32 messages
// until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		errCh <- e.messageLoop(ctx)
	}()
	return <-errCh
}

func (e *Engine) messageLoop(ctx context.Context) error {
	tidRet, _, _ := procGetCurrentThreadId.Call()
	threadID := uint32(tidRet)

	// ── WinEvent hook: EVENT_SYSTEM_ALERT ────────────────────────────────
	// Fires when a toast notification is shown (by any process).
	cbAlert := windows.NewCallback(e.onWinEvent)
	hAlert, _, _ := procSetWinEventHook.Call(
		uintptr(EVENT_SYSTEM_ALERT), uintptr(EVENT_SYSTEM_ALERT),
		0, cbAlert, 0, 0,
		uintptr(WINEVENT_OUTOFCONTEXT|WINEVENT_SKIPOWNTHREAD),
	)
	if hAlert == 0 {
		return fmt.Errorf("SetWinEventHook(EVENT_SYSTEM_ALERT) returned NULL")
	}
	defer procUnhookWinEvent.Call(hAlert)

	// ── WinEvent hook: EVENT_OBJECT_CREATE ───────────────────────────────
	// Catches notification windows that don't emit SYSTEM_ALERT.
	cbCreate := windows.NewCallback(e.onWinEvent)
	hCreate, _, _ := procSetWinEventHook.Call(
		uintptr(EVENT_OBJECT_CREATE), uintptr(EVENT_OBJECT_CREATE),
		0, cbCreate, 0, 0,
		uintptr(WINEVENT_OUTOFCONTEXT|WINEVENT_SKIPOWNTHREAD),
	)
	if hCreate != 0 {
		defer procUnhookWinEvent.Call(hCreate)
	}

	e.logger.Infof("Capture engine started — WinEvent hooks active on thread %d", threadID)

	// Action Center poll runs concurrently on a regular goroutine (not hook-thread).
	go func() {
		t := time.NewTicker(time.Duration(e.intervalMs) * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				e.pollActionCenter()
			}
		}
	}()

	// Break the message loop when context is cancelled.
	go func() {
		<-ctx.Done()
		procPostThreadMessageW.Call(uintptr(threadID), WM_QUIT, 0, 0)
	}()

	// Win32 message pump — keeps the hook alive.
	var msg msgStruct
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if r == 0 || r == ^uintptr(0) {
			break // WM_QUIT or error
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}

	e.logger.Info("Capture engine stopped")
	return nil
}

// onWinEvent is the WinEvent hook callback.
// Called on the hook thread for each EVENT_SYSTEM_ALERT / EVENT_OBJECT_CREATE.
// Signature matches WINEVENTPROC: (hook, event, hwnd, idObject, idChild, thread, time)
func (e *Engine) onWinEvent(hook, event uintptr, hwnd uintptr, idObject, idChild int32, eventThread, eventTime uint32) uintptr {
	if hwnd == 0 {
		return 0
	}

	className := winGetClassName(hwnd)

	// Toast popup window class names seen on Windows 10/11:
	isToastWindow :=
		strings.Contains(className, "Toast") ||
			strings.EqualFold(className, "Windows.UI.Core.CoreWindow") ||
			strings.Contains(className, "NativeHWNDHost") ||
			strings.Contains(className, "XamlExplicitAnimationTrigger")

	if !isToastWindow {
		return 0
	}

	e.extractAndEmit(hwnd, "")
	return 0
}

// pollActionCenter reads the currently open Action Center (notification flyout)
// and emits unseen notifications. This catches pre-existing notifications and
// apps that don't fire WinEvent hooks.
func (e *Engine) pollActionCenter() {
	hwnd := findNotificationWindow()
	if hwnd == 0 {
		return
	}
	e.extractAndEmit(hwnd, "ActionCenter")
}

// extractAndEmit reads all child window texts from hwnd, groups them into
// notifications, and emits any that pass filter + dedup.
func (e *Engine) extractAndEmit(hwnd uintptr, defaultApp string) {
	texts := childWindowTexts(hwnd)
	if len(texts) == 0 {
		return
	}

	// Filter out UI chrome strings
	chrome := map[string]bool{
		"clear all": true, "notifications": true, "notification center": true,
		"settings": true, "focus assist": true, "see all": true,
	}

	var clean []string
	for _, t := range texts {
		t = strings.TrimSpace(t)
		if t == "" || chrome[strings.ToLower(t)] {
			continue
		}
		clean = append(clean, t)
	}
	if len(clean) == 0 {
		return
	}

	appName := defaultApp
	if appName == "" {
		appName = winGetWindowText(hwnd)
		if appName == "" {
			appName = winGetClassName(hwnd)
		}
	}

	// Heuristic grouping: treat first text as title, next as body.
	// For the Action Center we may get many notifications; emit each pair.
	for i := 0; i < len(clean); i++ {
		title := clean[i]
		body := ""
		if i+1 < len(clean) {
			body = clean[i+1]
			i++ // consume body too
		}

		n := &Notification{
			ID:        uuid.New().String(),
			AppName:   appName,
			Title:     title,
			Body:      body,
			ArrivedAt: time.Now().UTC(),
		}

		if !e.shouldForward(n) || e.isDuplicate(n) {
			continue
		}

		e.logger.Infof("Captured [%s] %q — %q", n.AppName, n.Title, n.Body)
		select {
		case e.out <- n:
		default:
			e.logger.Warn("Output channel full, dropping: ", n.Title)
		}
	}
}

// ── De-duplication ────────────────────────────────────────────────────────────

func dedupKey(n *Notification) string {
	return strings.ToLower(n.Title) + "\x00" + strings.ToLower(n.Body)
}

func (e *Engine) isDuplicate(n *Notification) bool {
	key := dedupKey(n)
	now := time.Now()

	e.mu.Lock()
	defer e.mu.Unlock()

	// Prune stale entries (older than 60s)
	for k, t := range e.seen {
		if now.Sub(t) > 60*time.Second {
			delete(e.seen, k)
		}
	}

	if last, ok := e.seen[key]; ok && now.Sub(last) < 60*time.Second {
		return true
	}
	e.seen[key] = now
	return false
}

// ── Filter ────────────────────────────────────────────────────────────────────

func (e *Engine) shouldForward(n *Notification) bool {
	lower := strings.ToLower(n.AppName)
	if _, ignored := e.ignoreApps[lower]; ignored {
		return false
	}
	if len(e.filterApps) > 0 {
		_, ok := e.filterApps[lower]
		return ok
	}
	return true
}

// ── Win32 helpers ─────────────────────────────────────────────────────────────

func winGetWindowText(hwnd uintptr) string {
	buf := make([]uint16, 512)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf)
}

func winGetClassName(hwnd uintptr) string {
	buf := make([]uint16, 256)
	procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf)
}

// childWindowTexts enumerates all child windows of hwnd and returns their texts.
func childWindowTexts(hwnd uintptr) []string {
	var texts []string
	var mu sync.Mutex

	cb := windows.NewCallback(func(child, _ uintptr) uintptr {
		// Only visible windows have meaningful text
		vis, _, _ := procIsWindowVisible.Call(child)
		if vis == 0 {
			return 1
		}
		t := winGetWindowText(child)
		if t != "" {
			mu.Lock()
			texts = append(texts, t)
			mu.Unlock()
		}
		return 1 // continue
	})

	procEnumChildWindows.Call(hwnd, cb, 0)
	return texts
}

// findNotificationWindow tries known window class/title pairs for the Action Center.
func findNotificationWindow() uintptr {
	type candidate struct{ class, title string }
	candidates := []candidate{
		// Windows 10
		{"Windows.UI.Core.CoreWindow", "Notification Center"},
		// Windows 11
		{"TopLevelWindowForOverflowXamlIsland", ""},
		{"ActionCenter", ""},
		// Fallback
		{"Shell_TrayWnd", ""},
	}

	for _, c := range candidates {
		var classPtr, titlePtr *uint16
		if c.class != "" {
			p, _ := windows.UTF16PtrFromString(c.class)
			classPtr = p
		}
		if c.title != "" {
			p, _ := windows.UTF16PtrFromString(c.title)
			titlePtr = p
		}
		hwnd, _, _ := procFindWindowW.Call(
			ptrOrZero(classPtr),
			ptrOrZero(titlePtr),
		)
		if hwnd != 0 {
			return hwnd
		}
	}
	return 0
}

func ptrOrZero(p *uint16) uintptr {
	if p == nil {
		return 0
	}
	return uintptr(unsafe.Pointer(p))
}

// RequestAccess is a no-op: the WinEvent hook approach needs no consent dialog.
// Kept for --request-access CLI flag compatibility.
func RequestAccess(log *logrus.Logger) error {
	log.Info("WinEvent hook engine needs no special access grant — you can run normally.")
	return nil
}
