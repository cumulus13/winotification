package forwarder

// GrowlForwarder uses github.com/cumulus13/go-gntp v1.0.3
// API is read directly from the README:
//   - gntp.NewClient("App Name")          → *Client
//   - client.WithHost(host)               → *Client  (chained)
//   - client.WithPort(port)               → *Client
//   - client.WithIconMode(mode)           → *Client
//   - client.WithIcon(resource)           → *Client
//   - client.WithDebug(bool)              → *Client
//   - client.Register([]*NotificationType) → error
//   - client.Notify(name, title, text)    → error
//   - client.NotifyWithOptions(...)       → error
//   - client.SendMessage(*Message)        → error
//   - client.Close()                      → error
//
//   - gntp.NewNotificationType("name").WithDisplayName("...").WithIcon(res)
//   - gntp.LoadResource("icon.png")       → (*Resource, error)
//   - gntp.LoadResourceFromBytes(data, mime) → *Resource
//   - gntp.IconModeBinary                 (best for Windows Growl)
//   - gntp.IconModeDataURL
//   - gntp.IconModeFileURL
//   - gntp.IconModeHttpURL
//
//   Message struct (gntplib-compatible):
//     &gntp.Message{Event, Title, Text, Icon, Callback, DisplayName, Sticky, Priority}
//   client.SendMessage(msg) sends via the Message struct path.

import (
	"context"
	"fmt"

	gntp "github.com/cumulus13/go-gntp"
	"github.com/cumulus13/WiNotification/internal/capture"
	"github.com/cumulus13/WiNotification/internal/config"
	"github.com/sirupsen/logrus"
)

// GrowlForwarder sends notifications via the GNTP protocol to a Growl/Snarl server.
type GrowlForwarder struct {
	cfg    config.GrowlConfig
	log    *logrus.Logger
	client *gntp.Client
}

// NewGrowlForwarder builds a GNTP client using the cumulus13/go-gntp API,
// registers the WiNotification application, and returns a ready forwarder.
//
// Icon mode is set to Binary — the README confirms this is the most reliable
// mode for Windows Growl for Windows.
func NewGrowlForwarder(cfg config.GrowlConfig, log *logrus.Logger) (*GrowlForwarder, error) {
	// 1. Build client — method-chained fluent API per README.
	client := gntp.NewClient(cfg.AppName).
		WithHost(cfg.Host).
		WithPort(cfg.Port).
		WithIconMode(gntp.IconModeBinary) // Binary = most reliable on Windows Growl

	// if cfg.Password != "" {
	// 	// WithPassword is set on the struct directly if the library exposes it,
	// 	// otherwise the client handles auth internally. Per README there is no
	// 	// WithPassword chain method shown; set the field directly.
	// 	client.Password = cfg.Password
	// }

	// 2. Load app-level icon if configured.
	var appIcon *gntp.Resource
	if cfg.Icon != "" {
		res, err := gntp.LoadResource(cfg.Icon)
		if err != nil {
			log.WithError(err).Warnf("[growl] could not load icon %s — continuing without icon", cfg.Icon)
		} else {
			appIcon = res
			client.WithIcon(appIcon)
		}
	}

	// 3. Define notification types.
	//    We register three types matching Windows notification categories.
	notifAlert := gntp.NewNotificationType("alert").
		WithDisplayName("Alert")
	notifInfo := gntp.NewNotificationType("info").
		WithDisplayName("Information")
	notifWarning := gntp.NewNotificationType("warning").
		WithDisplayName("Warning")

	if appIcon != nil {
		notifAlert.WithIcon(appIcon)
		notifInfo.WithIcon(appIcon)
		notifWarning.WithIcon(appIcon)
	}

	// 4. Register — must be called before any Notify.
	if err := client.Register([]*gntp.NotificationType{
		notifAlert, notifInfo, notifWarning,
	}); err != nil {
		return nil, fmt.Errorf("growl register at %s:%d: %w", cfg.Host, cfg.Port, err)
	}

	log.Infof("[growl] registered '%s' with %s:%d (icon mode: Binary)", cfg.AppName, cfg.Host, cfg.Port)
	return &GrowlForwarder{cfg: cfg, log: log, client: client}, nil
}

func (g *GrowlForwarder) Name() string { return "growl" }

// Forward sends the notification via the Message struct API (gntplib-compatible path).
// Per the README:
//   msg := &gntp.Message{Event, Title, Text, Icon, ...}
//   client.SendMessage(msg)
func (g *GrowlForwarder) Forward(_ context.Context, n *capture.Notification) error {
	title := n.Title
	if title == "" {
		title = n.AppName
	}
	body := n.Body

	// Choose notification type: use "alert" for everything unless body is empty.
	event := "alert"

	msg := &gntp.Message{
		Event: event,
		Title: title,
		Text:  body,
	}

	// If this notification carried an icon (PNG bytes), attach it.
	if len(n.IconData) > 0 {
		// LoadResourceFromBytes: per README → gntp.LoadResourceFromBytes(data, mime)
		res := gntp.LoadResourceFromBytes(n.IconData, "image/png")
		// Message.Icon accepts a file path string per the gntplib-compatible struct.
		// For binary resources we use the client.NotifyWithOptions path instead.
		opts := gntp.NewNotifyOptions().WithIcon(res)
		return g.client.NotifyWithOptions(event, title, body, opts)
	}

	return g.client.SendMessage(msg)
}

// Close shuts down the GNTP callback listener if it was started.
func (g *GrowlForwarder) Close() error {
	return g.client.Close()
}
