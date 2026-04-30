// Package forwarder contains all notification forwarding backends.
// Author: Hadi Cahyadi <cumulus13@gmail.com>
package forwarder

import (
	"context"
	"sync"

	"github.com/cumulus13/WiNotification/internal/capture"
	"github.com/sirupsen/logrus"
)

// Forwarder is implemented by every output backend.
type Forwarder interface {
	Name() string
	Forward(ctx context.Context, n *capture.Notification) error
	Close() error
}

// Dispatcher fans-out notifications to all registered Forwarders.
// Forwarders can be added at any time via Add() — safe for concurrent use.
type Dispatcher struct {
	mu         sync.RWMutex
	forwarders []Forwarder
	log        *logrus.Logger
}

// NewDispatcher constructs a Dispatcher. Pass forwarders inline or add later via Add().
func NewDispatcher(log *logrus.Logger, fwds ...Forwarder) *Dispatcher {
	return &Dispatcher{
		forwarders: append([]Forwarder{}, fwds...),
		log:        log,
	}
}

// Add registers a new forwarder. Safe to call from any goroutine at any time,
// including after the dispatcher has started dispatching notifications.
func (d *Dispatcher) Add(f Forwarder) {
	d.mu.Lock()
	d.forwarders = append(d.forwarders, f)
	d.mu.Unlock()
	d.log.Infof("[dispatcher] forwarder added: %s", f.Name())
}

// Dispatch sends n to every registered forwarder concurrently.
func (d *Dispatcher) Dispatch(ctx context.Context, n *capture.Notification) {
	d.mu.RLock()
	fwds := make([]Forwarder, len(d.forwarders))
	copy(fwds, d.forwarders)
	d.mu.RUnlock()

	for _, f := range fwds {
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
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, f := range d.forwarders {
		if err := f.Close(); err != nil {
			d.log.WithError(err).Errorf("[%s] close error", f.Name())
		}
	}
}
