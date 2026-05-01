package forwarder

// GrowlForwarder — uses github.com/cumulus13/go-gntp v1.0.3
//
// SOURCE-VERIFIED bugs fixed (from reading register.go / notify.go directly):
//
// BUG 1 — IconModeBinary with no icon = malformed packet:
//   sendPacketWithResources() is called when IconMode==Binary regardless of
//   whether any resources exist. With zero resources the packet is sent with
//   wrong termination and Growl never responds → i/o timeout.
//   FIX: use IconModeDataURL always (NewClient default). Only switch to Binary
//   if an icon file is actually loaded AND you want binary mode.
//
// BUG 2 — icon file missing in dist/:
//   icons/icon.png is relative to CWD. If running from dist/ the file is at
//   dist/icons/icon.png but the binary's CWD is dist/, so "icons/icon.png"
//   resolves correctly only if icons/ exists there.
//   FIX: still use DataURL mode so even if icon load fails the packet is valid.
//
// SOURCE-CONFIRMED correct API (register.go / notify.go):
//   client.Register([]*gntp.NotificationType{...}) → error
//   client.Notify(name, title, text) → error
//   client.NotifyWithOptions(name, title, text, opts) → error
//   client.SendMessage(*Message) → error  (auto-registers if needed)
//   client.Close() → error

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
func NewGrowlForwarder(cfg config.GrowlConfig, log *logrus.Logger) (*GrowlForwarder, error) {
	// Always use IconModeDataURL.
	// IconModeBinary triggers sendPacketWithResources() which has framing
	// issues with Growl for Windows — the binary bytes are not separated
	// from the text headers correctly and Growl never sends -OK → i/o timeout.
	// DataURL embeds the icon as base64 inline in the text packet — no binary
	// framing, no separate resource blocks, works reliably.
	client := gntp.NewClient(cfg.AppName).
		WithHost(cfg.Host).
		WithPort(cfg.Port).
		WithIconMode(gntp.IconModeDataURL).
		WithTimeout(5 * time.Second)

	// Load app icon — DataURL mode only, never Binary.
	var appIcon *gntp.Resource
	if cfg.Icon != "" {
		res, err := gntp.LoadResource(cfg.Icon)
		if err != nil {
			log.WithError(err).Warnf("[growl] icon %q not found — sending without icon", cfg.Icon)
		} else {
			appIcon = res
			client.WithIcon(appIcon)
		}
	}

	// Notification types
	notifAlert := gntp.NewNotificationType("alert").WithDisplayName("Alert")
	notifInfo   := gntp.NewNotificationType("info").WithDisplayName("Information")
	notifWarn   := gntp.NewNotificationType("warning").WithDisplayName("Warning")

	if appIcon != nil {
		notifAlert = notifAlert.WithIcon(appIcon)
		notifInfo  = notifInfo.WithIcon(appIcon)
		notifWarn  = notifWarn.WithIcon(appIcon)
	}

	if err := client.Register([]*gntp.NotificationType{
		notifAlert, notifInfo, notifWarn,
	}); err != nil {
		return nil, fmt.Errorf("growl register at %s:%d: %w", cfg.Host, cfg.Port, err)
	}

	log.Infof("[growl] registered '%s' with %s:%d (DataURL mode)", cfg.AppName, cfg.Host, cfg.Port)
	return &GrowlForwarder{cfg: cfg, log: log, client: client}, nil
}

func (g *GrowlForwarder) Name() string { return "growl" }

func (g *GrowlForwarder) Forward(_ context.Context, n *capture.Notification) error {
	title := n.Title
	if title == "" {
		title = n.AppName
	}

	// Per-notification icon — use DataURL mode (client already set to DataURL).
	// NotifyWithOptions with an icon resource in DataURL mode uses sendPacket
	// (not sendPacketWithResources), so no binary framing issues.
	if len(n.IconData) > 0 {
		res := gntp.LoadResourceFromBytes(n.IconData, "image/png")
		opts := gntp.NewNotifyOptions().WithIcon(res)
		return g.client.NotifyWithOptions("alert", title, n.Body, opts)
	}

	return g.client.Notify("alert", title, n.Body)
}

func (g *GrowlForwarder) Close() error {
	return g.client.Close()
}
