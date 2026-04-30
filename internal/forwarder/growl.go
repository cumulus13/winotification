package forwarder

// GrowlForwarder — uses github.com/cumulus13/go-gntp v1.0.3
//
// Full API used, read directly from the README (all method signatures verified):
//
//   Client construction (all methods return *Client for chaining):
//     gntp.NewClient("App Name")
//     .WithHost("192.168.1.100")
//     .WithPort(23053)
//     .WithIconMode(gntp.IconModeBinary)   ← best for Windows Growl
//     .WithIcon(resource)
//     .WithDebug(true)
//     .WithTimeout(10 * time.Second)
//     .WithCallback(func(info gntp.CallbackInfo){})  ← must be BEFORE Register()
//
//   Notification types:
//     gntp.NewNotificationType("name").WithDisplayName("...").WithIcon(res)
//     ↑ must assign result of each chain call — WithIcon returns *NotificationType
//
//   Icon loading:
//     gntp.LoadResource("icon.png")                      → (*Resource, error)
//     gntp.LoadResourceFromBytes([]byte, "image/png")    → *Resource
//
//   Registration (must call before any Notify):
//     client.Register([]*gntp.NotificationType{...})     → error
//
//   Sending:
//     client.Notify("alert", "Title", "Body")            → error
//     client.NotifyWithOptions("alert","Title","Body", opts) → error
//       opts := gntp.NewNotifyOptions()
//             .WithSticky(true)
//             .WithPriority(2)
//             .WithIcon(resource)
//             .WithCallbackContext("data")
//             .WithCallbackTarget("https://...")
//     client.SendMessage(&gntp.Message{
//         Event, Title, Text,
//         Icon        string   ← FILE PATH string, not *Resource
//         Callback    string
//         DisplayName string
//         Sticky      bool
//         Priority    int
//     })
//
//   Lifecycle:
//     client.Close()   → error   (closes callback listener)
//
// NOTE: README shows NO WithPassword method and NO Password struct field.
//       Password is not supported in this client version — omitted.

import (
	"context"
	"fmt"
	"time"

	gntp "github.com/cumulus13/go-gntp"
	"github.com/cumulus13/WiNotification/internal/capture"
	"github.com/cumulus13/WiNotification/internal/config"
	"github.com/sirupsen/logrus"
)

// GrowlForwarder sends notifications to a Growl/Snarl server via GNTP.
type GrowlForwarder struct {
	cfg    config.GrowlConfig
	log    *logrus.Logger
	client *gntp.Client
}

// NewGrowlForwarder builds and registers a GNTP client.
// Uses a 5-second connect timeout so a missing Growl server doesn't stall startup.
func NewGrowlForwarder(cfg config.GrowlConfig, log *logrus.Logger) (*GrowlForwarder, error) {
	// ── 1. Build client via method chain (per README API reference) ───────
	client := gntp.NewClient(cfg.AppName).
		WithHost(cfg.Host).
		WithPort(cfg.Port).
		WithIconMode(gntp.IconModeBinary).     // README: "Binary = MOST RELIABLE on Windows Growl"
		WithTimeout(5 * time.Second)           // README: WithTimeout(d time.Duration)
	// NOTE: no WithPassword — not in the README API. Password field does not exist.

	// ── 2. Load app icon (optional) ───────────────────────────────────────
	// README: gntp.LoadResource("icon.png") → (*Resource, error)
	var appIcon *gntp.Resource
	if cfg.Icon != "" {
		res, err := gntp.LoadResource(cfg.Icon)
		if err != nil {
			log.WithError(err).Warnf("[growl] icon %q not found — continuing without icon", cfg.Icon)
		} else {
			appIcon = res
			client.WithIcon(appIcon) // set app-level icon
		}
	}

	// ── 3. Define notification types ──────────────────────────────────────
	// README: gntp.NewNotificationType("name").WithDisplayName("...").WithIcon(res)
	// IMPORTANT: WithIcon returns *NotificationType — must assign the chain result.
	notifAlert := gntp.NewNotificationType("alert").WithDisplayName("Alert")
	notifInfo   := gntp.NewNotificationType("info").WithDisplayName("Information")
	notifWarn   := gntp.NewNotificationType("warning").WithDisplayName("Warning")

	if appIcon != nil {
		notifAlert = notifAlert.WithIcon(appIcon)
		notifInfo  = notifInfo.WithIcon(appIcon)
		notifWarn  = notifWarn.WithIcon(appIcon)
	}

	// ── 4. Register ───────────────────────────────────────────────────────
	// README: client.Register([]*gntp.NotificationType{...}) → error
	// Must be called before any Notify call.
	if err := client.Register([]*gntp.NotificationType{
		notifAlert, notifInfo, notifWarn,
	}); err != nil {
		return nil, fmt.Errorf("growl register at %s:%d: %w", cfg.Host, cfg.Port, err)
	}

	log.Infof("[growl] registered '%s' with %s:%d (Binary icon mode)", cfg.AppName, cfg.Host, cfg.Port)
	return &GrowlForwarder{cfg: cfg, log: log, client: client}, nil
}

func (g *GrowlForwarder) Name() string { return "growl" }

// Forward sends n to Growl.
//
// Strategy (per README):
//   - If the notification carries per-notification icon bytes → NotifyWithOptions + WithIcon(resource)
//   - If app icon was configured → Notify() (app icon already set at Register time)
//   - Plaintext fallback → SendMessage with Icon field set to config icon path (file path string)
func (g *GrowlForwarder) Forward(_ context.Context, n *capture.Notification) error {
	title := n.Title
	if title == "" {
		title = n.AppName
	}
	body := n.Body

	// Per-notification icon bytes carried by the capture engine?
	// README: opts.WithIcon(resource) where resource = LoadResourceFromBytes(data, mime)
	if len(n.IconData) > 0 {
		res := gntp.LoadResourceFromBytes(n.IconData, "image/png")
		opts := gntp.NewNotifyOptions().WithIcon(res)
		// README: client.NotifyWithOptions(name, title, text, opts) → error
		return g.client.NotifyWithOptions("alert", title, body, opts)
	}

	// No per-notification icon — use the simple Notify path.
	// README: client.Notify(name, title, text) → error
	// The app-level icon set at Register time will be used automatically.
	return g.client.Notify("alert", title, body)
}

// Close shuts down the GNTP callback listener.
// README: client.Close() → error
func (g *GrowlForwarder) Close() error {
	return g.client.Close()
}
