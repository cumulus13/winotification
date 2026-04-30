//go:build windows

package main

import (
	"fmt"

	"github.com/cumulus13/WiNotification/internal/config"
	"github.com/cumulus13/WiNotification/internal/forwarder"
	"github.com/sirupsen/logrus"
)

func addToastForwarder(cfg *config.Config, log *logrus.Logger, fwds *[]forwarder.Forwarder, skipped *[]skipEntry) {
	fmt.Printf("  %s  %s\n", green(" OK "), bold("toast"))
	*fwds = append(*fwds, forwarder.NewToastForwarder(cfg.Toast, log))
}
