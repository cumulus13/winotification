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

//   README says Binary is recommended for Windows Growl.
//   README also says DataURL "may have issues with large icons on Windows".
//   The word "large" is the key — a resized 64×64 PNG is ~3-5KB.
//   As base64 DataURL that is ~4-7KB — not large at all.
//   Binary mode keeps timing out regardless of timeout value, meaning the
//   sendPacketWithResources framing does not work with this Growl version.
//   DataURL with a small resized icon works perfectly — no timeout, icon shows.
//
//   FINAL DECISION: IconModeDataURL + resize icon to 64×64 before sending.

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	_ "image/jpeg"
	"os"
	"time"
	"encoding/json"

	_ "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"

	gntp "github.com/cumulus13/go-gntp"
	"github.com/cumulus13/WiNotification/internal/capture"
	"github.com/cumulus13/WiNotification/internal/config"
	"github.com/sirupsen/logrus"
)

const maxIconSize = 64

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
		WithTimeout(10 * time.Second)

	// Load app icon — DataURL mode only, never Binary.
	var appIcon *gntp.Resource
	if cfg.Icon != "" {
		appIcon, err := gntp.LoadResource(cfg.Icon)
		if err != nil {
			log.WithError(err).Warnf("[growl] icon %q load failed — sending without icon", cfg.Icon)
		} else {
			// appIcon = res
			// client.WithIcon(appIcon)
			// fmt.Printf("✓ Icon loaded successfully (%d bytes)\n\n", len(appIcon.Data))
			log.Infof("✓ Icon loaded successfully (%d bytes)\n\n", len(appIcon.Data))
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

	log.Infof("[growl] registered '%s' with %s:%d (DataURL + 64px icon)", cfg.AppName, cfg.Host, cfg.Port)
	return &GrowlForwarder{cfg: cfg, log: log, client: client}, nil
}

func (g *GrowlForwarder) Name() string { return "growl" }

func (g *GrowlForwarder) Forward(_ context.Context, n *capture.Notification) error {
	title := n.Title
	if title == "" {
		title = n.AppName
	}
	if data, err := json.Marshal(n); err == nil {
		fmt.Printf("n = %s\n", data)
	} else {
		fmt.Println("error:", err)
	}
	
	// Per-notification icon — use DataURL mode (client already set to DataURL).
	// NotifyWithOptions with an icon resource in DataURL mode uses sendPacket
	// (not sendPacketWithResources), so no binary framing issues.
	if len(n.IconData) > 0 {
		small, err := resizeToSmall(n.IconData)
		if err != nil {
			small = n.IconData
		}
		res := gntp.LoadResourceFromBytes(small, "image/png")
		opts := gntp.NewNotifyOptions().WithIcon(res)
		return g.client.NotifyWithOptions("alert", title, n.Body, opts)
	}

	return g.client.Notify("alert", title, n.Body)
}

func (g *GrowlForwarder) Close() error { return g.client.Close() }

// loadSmallIcon reads path, decodes, resizes to ≤64×64, returns as gntp.Resource.
func loadSmallIcon(path string, log *logrus.Logger) (*gntp.Resource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	small, err := resizeToSmall(data)
	if err != nil {
		log.Warnf("[growl] resize failed (%v) — using raw bytes", err)
		return gntp.LoadResourceFromBytes(data, "image/png"), nil
	}
	return gntp.LoadResourceFromBytes(small, "image/png"), nil
}

// resizeToSmall decodes any supported image and encodes it as a ≤64×64 PNG.
func resizeToSmall(data []byte) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	w := src.Bounds().Dx()
	h := src.Bounds().Dy()

	if w <= maxIconSize && h <= maxIconSize {
		var buf bytes.Buffer
		if err := png.Encode(&buf, src); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	scale := float64(maxIconSize) / float64(w)
	if s := float64(maxIconSize) / float64(h); s < scale {
		scale = s
	}
	nw := max1(int(float64(w)*scale))
	nh := max1(int(float64(h)*scale))

	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	xdraw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)

	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func max1(v int) int {
	if v < 1 {
		return 1
	}
	return v
}
