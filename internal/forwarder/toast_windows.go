//go:build windows

package forwarder

import (
	"context"

	"github.com/cumulus13/WiNotification/internal/capture"
	"github.com/cumulus13/WiNotification/internal/config"
	"github.com/go-toast/toast"
	"github.com/sirupsen/logrus"
)

// ToastForwarder re-broadcasts captured notifications as new Windows toast
// notifications (useful for filtering, de-duplicating, or routing).
type ToastForwarder struct {
	cfg config.ToastConfig
	log *logrus.Logger
}

// NewToastForwarder returns a configured toast re-broadcaster.
func NewToastForwarder(cfg config.ToastConfig, log *logrus.Logger) *ToastForwarder {
	log.Info("[toast] forwarder ready")
	return &ToastForwarder{cfg: cfg, log: log}
}

func (t *ToastForwarder) Name() string { return "toast" }

func (t *ToastForwarder) Forward(_ context.Context, n *capture.Notification) error {
	title := n.Title
	if title == "" {
		title = n.AppName
	}

	// Handle audio with error checking (don't ignore the error)
	audio, err := toast.Audio(t.cfg.Audio)
	if err != nil {
		// Handle error appropriately, maybe use default audio or no audio
		audio = toast.Default // or toast.NoAudio
	}

	// Handle duration with proper error checking using the API
	duration, err := toast.Duration(t.cfg.Duration)
	if err != nil {
		// If invalid duration, default to Short
		duration = toast.Short
	}

	notification := toast.Notification{
		AppID:   t.cfg.AppID,
		Title:   title,
		Message: n.Body,
		// Audio:   toast.Audio(t.cfg.Audio),
		Audio:   audio,
		// Duration: func() toast.Duration {
		// 	if t.cfg.Duration == "long" {
		// 		return toast.Long
		// 	}
		// 	return toast.Short
		// }(),
		Duration: duration,
	}

	return notification.Push()
}

func (t *ToastForwarder) Close() error { return nil }
