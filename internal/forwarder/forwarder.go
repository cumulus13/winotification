// Package forwarder contains all notification forwarding backends.
// Author: Hadi Cahyadi <cumulus13@gmail.com>
package forwarder

import (
	"context"

	"github.com/cumulus13/WiNotification/internal/capture"
	"github.com/sirupsen/logrus"
)

// Forwarder is implemented by every output backend.
type Forwarder interface {
	// Name returns the human-readable name of this forwarder.
	Name() string
	// Forward sends a notification to the backend.
	Forward(ctx context.Context, n *capture.Notification) error
	// Close releases resources held by this forwarder.
	Close() error
}

// Dispatcher fans-out notifications to all enabled Forwarders.
type Dispatcher struct {
	forwarders []Forwarder
	log        *logrus.Logger
}

// NewDispatcher constructs a Dispatcher with the given backends.
func NewDispatcher(log *logrus.Logger, fwds ...Forwarder) *Dispatcher {
	return &Dispatcher{forwarders: fwds, log: log}
}

// Dispatch sends n to every registered forwarder concurrently.
func (d *Dispatcher) Dispatch(ctx context.Context, n *capture.Notification) {
	for _, f := range d.forwarders {
		go func(fwd Forwarder) {
			if err := fwd.Forward(ctx, n); err != nil {
				d.log.WithError(err).Errorf("[%s] forward error", fwd.Name())
			} else {
				d.log.Debugf("[%s] forwarded: %s — %s", fwd.Name(), n.AppName, n.Title)
			}
		}(f)
	}
}

// Close shuts down all forwarders.
func (d *Dispatcher) Close() {
	for _, f := range d.forwarders {
		if err := f.Close(); err != nil {
			d.log.WithError(err).Errorf("[%s] close error", f.Name())
		}
	}
}
