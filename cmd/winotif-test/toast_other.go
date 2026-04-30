//go:build !windows

package main

import (
	"errors"
	"fmt"

	"github.com/cumulus13/WiNotification/internal/config"
	"github.com/cumulus13/WiNotification/internal/forwarder"
	"github.com/sirupsen/logrus"
)

func addToastForwarder(cfg *config.Config, _ *logrus.Logger, _ *[]forwarder.Forwarder, skipped *[]skipEntry) {
	err := errors.New("toast is Windows-only — skipped on this platform")
	fmt.Printf("  %s  %s %s\n", yellow("SKIP"), bold("toast"), dim(err.Error()))
	*skipped = append(*skipped, skipEntry{name: "toast", err: err})
}
