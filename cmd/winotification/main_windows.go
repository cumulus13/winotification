//go:build windows

// WiNotification — Windows notification forwarder
//
// Captures all Windows Action Center notifications and forwards them to
// configurable backends: Growl (GNTP), ntfy, RabbitMQ, ZeroMQ, Redis,
// SQL database, and Windows Toast re-broadcast.
//
// Author:   Hadi Cahyadi <cumulus13@gmail.com>
// Homepage: github.com/cumulus13/WiNotification
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/cumulus13/WiNotification/internal/capture"
	"github.com/cumulus13/WiNotification/internal/config"
	"github.com/cumulus13/WiNotification/internal/forwarder"
	applogger "github.com/cumulus13/WiNotification/internal/logger"
	appsystray "github.com/cumulus13/WiNotification/internal/systray"
)

const version = "1.0.0"

func main() {
	cfgFile := flag.String("config", "config.toml", "Path to configuration file")
	showVer := flag.Bool("version", false, "Print version and exit")
	// --request-access kept for backwards compatibility but is now a no-op:
	// the WinEvent hook engine requires no UWP package identity or user consent.
	reqAccess := flag.Bool("request-access", false, "(no-op) Previously required for UserNotificationListener; not needed with WinEvent hook engine")
	flag.Parse()

	if *showVer {
		fmt.Printf("WiNotification v%s\nAuthor: Hadi Cahyadi <cumulus13@gmail.com>\nhomepage: github.com/cumulus13/WiNotification\n", version)
		os.Exit(0)
	}

	// ── Load config ──────────────────────────────────────────
	cfg, err := config.Load(*cfgFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Config error: %v\n", err)
		os.Exit(1)
	}

	// ── Logger ───────────────────────────────────────────────
	log := applogger.New(cfg.General.LogLevel, cfg.General.LogFile)
	log.Infof("WiNotification v%s starting", version)

	// ── One-shot: request notification access ────────────────
	if *reqAccess {
		capture.RequestAccess(log) // prints informational message and returns
		os.Exit(0)
	}

	// ── Build forwarders ─────────────────────────────────────
	// Growl is initialised in a background goroutine so that a slow or
	// unreachable Growl server does not block startup. All other forwarders
	// connect inline with their own short timeouts (Redis 5s, etc.).
	dispatcher := forwarder.NewDispatcher(log)
	defer dispatcher.Close()

	// Add all non-blocking forwarders immediately
	if cfg.Ntfy.Enabled {
		dispatcher.Add(forwarder.NewNtfyForwarder(cfg.Ntfy, log))
	}

	if cfg.Toast.Enabled {
		dispatcher.Add(forwarder.NewToastForwarder(cfg.Toast, log))
	}

	if cfg.RabbitMQ.Enabled {
		rf, err := forwarder.NewRabbitMQForwarder(cfg.RabbitMQ, log)
		if err != nil {
			log.WithError(err).Warn("RabbitMQ forwarder disabled")
		} else {
			dispatcher.Add(rf)
		}
	}

	if cfg.ZeroMQ.Enabled {
		zf, err := forwarder.NewZeroMQForwarder(cfg.ZeroMQ, log)
		if err != nil {
			log.WithError(err).Warn("ZeroMQ forwarder disabled")
		} else {
			dispatcher.Add(zf)
		}
	}

	if cfg.Redis.Enabled {
		rdf, err := forwarder.NewRedisForwarder(cfg.Redis, log)
		if err != nil {
			log.WithError(err).Warn("Redis forwarder disabled")
		} else {
			dispatcher.Add(rdf)
		}
	}

	if cfg.Database.Enabled {
		dbf, err := forwarder.NewDBForwarder(cfg.Database, log)
		if err != nil {
			log.WithError(err).Warn("Database forwarder disabled")
		} else {
			dispatcher.Add(dbf)
		}
	}

	// Growl: connect in background — Register() blocks for up to 5s (TCP timeout)
	if cfg.Growl.Enabled {
		go func() {
			gf, err := forwarder.NewGrowlForwarder(cfg.Growl, log)
			if err != nil {
				log.WithError(err).Warn("Growl forwarder disabled (connection failed)")
				return
			}
			dispatcher.Add(gf)
			log.Info("Growl forwarder connected and ready")
		}()
	}

	// ── Context + systray ────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tray := appsystray.NewManager(cfg.General.IconPath, log, cancel)

	// Notification channel (buffered to absorb bursts)
	notifCh := make(chan *capture.Notification, 256)

	// Capture engine
	engine := capture.NewEngine(
		cfg.General.CaptureIntervalMs,
		cfg.General.FilterApps,
		cfg.General.IgnoreApps,
		notifCh,
		log,
	)

	// ── Goroutine: capture loop ───────────────────────────────
	go func() {
		paused := false
		stopped := false

		for {
			select {
			case <-ctx.Done():
				return

			case <-tray.PauseCh():
				paused = !paused
				if paused {
					log.Info("Capture paused by user")
				} else {
					log.Info("Capture resumed by user")
				}

			case <-tray.StopCh():
				stopped = !stopped
				if stopped {
					log.Info("Capture stopped by user")
				} else {
					stopped = false
					log.Info("Capture restarted by user")
					go func() {
						if err := engine.Run(ctx); err != nil && ctx.Err() == nil {
							log.WithError(err).Error("Capture engine error")
						}
					}()
				}

			case n, ok := <-notifCh:
				if !ok {
					return
				}
				if paused || stopped {
					continue
				}
				log.Infof("Captured [%s] %s: %s", n.AppName, n.Title, n.Body)
				dispatcher.Dispatch(ctx, n)
			}
		}
	}()

	// ── Start capture engine ──────────────────────────────────
	go func() {
		if err := engine.Run(ctx); err != nil && ctx.Err() == nil {
			log.WithError(err).Error("Capture engine terminated unexpectedly")
		}
	}()

	log.Info("WiNotification running. Right-click the tray icon to control.")

	// ── Systray blocks until Quit ─────────────────────────────
	tray.Run()
	log.Info("WiNotification exited cleanly")
}
