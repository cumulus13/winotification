package forwarder

// GrowlForwarder — uses github.com/cumulus13/go-gntp v1.0.3
//
// README confirmed (re-read in full before writing this):
//
//   Icon modes per README compatibility table:
//     Windows Growl — Binary ✅ RECOMMENDED, DataURL ⚠️ issues
//   README says explicitly:
//     "Binary Mode (Recommended for Windows!)"
//     "✅ Tested and working on Windows Growl"
//     DataURL: "⚠️ May have issues with large icons on Windows"
//
//   Previous DataURL choice was wrong — it caused icons not to show.
//   Previous Binary timeout was caused by icon.png being 538KB (too large),
//   not a framing bug. sendPacketWithResources() framing is correct per spec.
//
//   Fix:
//     - Use IconModeBinary (correct per README)
//     - Resize icon to ≤64×64 PNG before loading so binary transfer is fast
//     - Raise timeout to 15s to accommodate binary transfer on localhost
//     - Fall back gracefully if icon load/resize fails (no icon, still works)
//
//   API used (README-confirmed):
//     gntp.NewClient(name).WithHost().WithPort().WithIconMode().WithIcon().WithTimeout()
//     gntp.LoadResource(path)            → (*Resource, error)
//     gntp.LoadResourceFromBytes([]byte, mime) → *Resource
//     gntp.NewNotificationType(name).WithDisplayName(s).WithIcon(res)
//     client.Register([]*NotificationType) → error
//     client.Notify(name, title, text)   → error
//     client.NotifyWithOptions(name, title, text, opts) → error
//     gntp.NewNotifyOptions().WithIcon(res)
//     client.Close()                     → error

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	_ "image/jpeg"
	"os"
	"time"

	_ "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"

	gntp "github.com/cumulus13/go-gntp"
	"github.com/cumulus13/WiNotification/internal/capture"
	"github.com/cumulus13/WiNotification/internal/config"
	"github.com/sirupsen/logrus"
)

// maxIconSize is the maximum dimension (width or height) we send to Growl.
// Large icons cause slow binary transfer and Growl may ignore them.
const maxIconSize = 64

// GrowlForwarder sends notifications to a Growl/Snarl server via GNTP.
type GrowlForwarder struct {
	cfg    config.GrowlConfig
	log    *logrus.Logger
	client *gntp.Client
}

// NewGrowlForwarder builds and registers a GNTP client using Binary icon mode
// as recommended by the go-gntp README for Windows Growl.
func NewGrowlForwarder(cfg config.GrowlConfig, log *logrus.Logger) (*GrowlForwarder, error) {
	// README: Binary mode is RECOMMENDED for Windows Growl.
	// Use 15s timeout — binary transfer of icon data on localhost is fast
	// but we want headroom; previous 5s was too tight for any icon at all.
	client := gntp.NewClient(cfg.AppName).
		WithHost(cfg.Host).
		WithPort(cfg.Port).
		WithIconMode(gntp.IconModeBinary).
		WithTimeout(15 * time.Second)

	// Load and resize the app icon.
	// README: gntp.LoadResource(path) → (*Resource, error)
	var appIcon *gntp.Resource
	if cfg.Icon != "" {
		res, err := loadResizedIcon(cfg.Icon, log)
		if err != nil {
			log.WithError(err).Warnf("[growl] icon load failed — registering without icon")
		} else {
			appIcon = res
			client.WithIcon(appIcon)
		}
	}

	// README: gntp.NewNotificationType(name).WithDisplayName(s).WithIcon(res)
	// Each chain method returns *NotificationType — assign the result.
	notifAlert := gntp.NewNotificationType("alert").WithDisplayName("Alert")
	notifInfo   := gntp.NewNotificationType("info").WithDisplayName("Information")
	notifWarn   := gntp.NewNotificationType("warning").WithDisplayName("Warning")

	if appIcon != nil {
		notifAlert = notifAlert.WithIcon(appIcon)
		notifInfo  = notifInfo.WithIcon(appIcon)
		notifWarn  = notifWarn.WithIcon(appIcon)
	}

	// README: client.Register([]*gntp.NotificationType{...}) → error
	// Must be called before any Notify.
	if err := client.Register([]*gntp.NotificationType{
		notifAlert, notifInfo, notifWarn,
	}); err != nil {
		return nil, fmt.Errorf("growl register at %s:%d: %w", cfg.Host, cfg.Port, err)
	}

	log.Infof("[growl] registered '%s' with %s:%d (Binary mode)", cfg.AppName, cfg.Host, cfg.Port)
	return &GrowlForwarder{cfg: cfg, log: log, client: client}, nil
}

func (g *GrowlForwarder) Name() string { return "growl" }

// Forward sends the notification to Growl.
// README: client.Notify(name, title, text) → error
//         client.NotifyWithOptions(name, title, text, opts) → error
func (g *GrowlForwarder) Forward(_ context.Context, n *capture.Notification) error {
	title := n.Title
	if title == "" {
		title = n.AppName
	}

	// If capture engine extracted a per-notification icon, attach it.
	// README: gntp.LoadResourceFromBytes(data, mime) → *Resource
	//         gntp.NewNotifyOptions().WithIcon(res)
	if len(n.IconData) > 0 {
		resized, err := resizePNGBytes(n.IconData)
		if err != nil {
			resized = n.IconData // use original if resize fails
		}
		res := gntp.LoadResourceFromBytes(resized, "image/png")
		opts := gntp.NewNotifyOptions().WithIcon(res)
		return g.client.NotifyWithOptions("alert", title, n.Body, opts)
	}

	// README: client.Notify(name, title, text) → error
	return g.client.Notify("alert", title, n.Body)
}

// Close shuts down the GNTP callback listener.
// README: client.Close() → error
func (g *GrowlForwarder) Close() error {
	return g.client.Close()
}

// ── Icon helpers ──────────────────────────────────────────────────────────────

// loadResizedIcon reads an image file, resizes it to ≤64×64, encodes as PNG,
// and returns a gntp.Resource. Supports PNG, JPEG, BMP (via stdlib + x/image).
func loadResizedIcon(path string, log *logrus.Logger) (*gntp.Resource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	resized, err := resizePNGBytes(data)
	if err != nil {
		// If decode/resize fails (e.g. ICO format), use raw bytes as-is.
		log.Warnf("[growl] icon resize failed (%v) — using raw bytes", err)
		return gntp.LoadResourceFromBytes(data, "image/png"), nil
	}

	// README: gntp.LoadResourceFromBytes(data []byte, mimeType string) → *Resource
	return gntp.LoadResourceFromBytes(resized, "image/png"), nil
}

// resizePNGBytes decodes an image from b, scales it to fit within maxIconSize,
// and re-encodes as PNG bytes.
func resizePNGBytes(b []byte) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	// Only resize if larger than maxIconSize.
	if w <= maxIconSize && h <= maxIconSize {
		// Already small enough — re-encode as PNG to normalise format.
		var buf bytes.Buffer
		if err := png.Encode(&buf, src); err != nil {
			return nil, fmt.Errorf("encode: %w", err)
		}
		return buf.Bytes(), nil
	}

	// Scale down proportionally.
	scale := float64(maxIconSize) / float64(w)
	if hScale := float64(maxIconSize) / float64(h); hScale < scale {
		scale = hScale
	}
	newW := int(float64(w) * scale)
	newH := int(float64(h) * scale)
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	// golang.org/x/image/draw.BiLinear gives good quality for downscaling.
	xdraw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)

	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, fmt.Errorf("encode resized: %w", err)
	}
	return buf.Bytes(), nil
}
