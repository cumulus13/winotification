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
	reqAccess := flag.Bool("request-access", false, "Request Windows notification access and exit")
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
		if err := capture.RequestAccess(log); err != nil {
			log.WithError(err).Fatal("Could not request notification access")
		}
		log.Info("Access requested. Please accept the system prompt then re-run without --request-access.")
		os.Exit(0)
	}

	// ── Build forwarders ─────────────────────────────────────
	var fwds []forwarder.Forwarder

	if cfg.Growl.Enabled {
		gf, err := forwarder.NewGrowlForwarder(cfg.Growl, log)
		if err != nil {
			log.WithError(err).Warn("Growl forwarder disabled (connection failed)")
		} else {
			fwds = append(fwds, gf)
		}
	}

	if cfg.Ntfy.Enabled {
		fwds = append(fwds, forwarder.NewNtfyForwarder(cfg.Ntfy, log))
	}

	if cfg.RabbitMQ.Enabled {
		rf, err := forwarder.NewRabbitMQForwarder(cfg.RabbitMQ, log)
		if err != nil {
			log.WithError(err).Warn("RabbitMQ forwarder disabled")
		} else {
			fwds = append(fwds, rf)
		}
	}

	if cfg.ZeroMQ.Enabled {
		zf, err := forwarder.NewZeroMQForwarder(cfg.ZeroMQ, log)
		if err != nil {
			log.WithError(err).Warn("ZeroMQ forwarder disabled")
		} else {
			fwds = append(fwds, zf)
		}
	}

	if cfg.Redis.Enabled {
		rdf, err := forwarder.NewRedisForwarder(cfg.Redis, log)
		if err != nil {
			log.WithError(err).Warn("Redis forwarder disabled")
		} else {
			fwds = append(fwds, rdf)
		}
	}

	if cfg.Database.Enabled {
		dbf, err := forwarder.NewDBForwarder(cfg.Database, log)
		if err != nil {
			log.WithError(err).Warn("Database forwarder disabled")
		} else {
			fwds = append(fwds, dbf)
		}
	}

	if cfg.Toast.Enabled {
		fwds = append(fwds, forwarder.NewToastForwarder(cfg.Toast, log))
	}

	if len(fwds) == 0 {
		log.Warn("No forwarders are enabled — notifications will be captured but not forwarded")
	}

	// ── Dispatcher ───────────────────────────────────────────
	dispatcher := forwarder.NewDispatcher(log, fwds...)
	defer dispatcher.Close()

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
