// winotif-test — CLI test tool for WiNotification forwarder backends.
//
// Sends a synthetic notification through each enabled (or explicitly targeted)
// backend and reports pass/fail with timing. No Windows-only code — runs on
// any platform, including from a Linux/WSL shell.
//
// Usage:
//
//	winotif-test [flags]
//	winotif-test --target growl,ntfy
//	winotif-test --target all --config path/to/config.toml
//	winotif-test --list
//	winotif-test --watch  (send one notification every 5s until Ctrl-C)
//
// Author:   Hadi Cahyadi <cumulus13@gmail.com>
// Homepage: github.com/cumulus13/WiNotification
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cumulus13/WiNotification/internal/capture"
	"github.com/cumulus13/WiNotification/internal/config"
	applogger "github.com/cumulus13/WiNotification/internal/logger"
	"github.com/cumulus13/WiNotification/internal/forwarder"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// ── ANSI colours ─────────────────────────────────────────────────────────────

var noColorMode bool

func init() {
	// Honour the NO_COLOR standard (https://no-color.org/)
	if os.Getenv("NO_COLOR") != "" {
		noColorMode = true
	}
}

func col(code, s string) string {
	if noColorMode {
		return s
	}
	return code + s + "\033[0m"
}

func green(s string) string  { return col("\033[32m", s) }
func red(s string) string    { return col("\033[31m", s) }
func yellow(s string) string { return col("\033[33m", s) }
func cyan(s string) string   { return col("\033[36m", s) }
func bold(s string) string   { return col("\033[1m", s) }
func dim(s string) string    { return col("\033[2m", s) }

// ── Result ────────────────────────────────────────────────────────────────────

type result struct {
	name    string
	ok      bool
	elapsed time.Duration
	err     error
}

// ── All known backend names ───────────────────────────────────────────────────

var allTargets = []string{"growl", "ntfy", "rabbitmq", "zeromq", "redis", "database", "toast"}

// ── main ─────────────────────────────────────────────────────────────────────

func main() {
	cfgFile  := flag.String("config",  "config.toml", "Path to config.toml")
	targetF  := flag.String("target",  "enabled",     "Backends to test: all | enabled | growl,ntfy,redis,… (comma-separated)")
	listF    := flag.Bool("list",      false,          "List available backends and their enabled state, then exit")
	watchF   := flag.Bool("watch",     false,          "Keep sending a notification every --interval seconds until Ctrl-C")
	interval := flag.Duration("interval", 5*time.Second, "Interval for --watch mode")
	appName  := flag.String("app",     "WiNotification Test", "Fake app name on the notification")
	title    := flag.String("title",   "Test Notification",   "Notification title")
	body     := flag.String("body",    "This is a test notification sent by winotif-test.", "Notification body")
	verbose  := flag.Bool("verbose",   false, "Show debug log output from backends")
	noColor  := flag.Bool("no-color",  false, "Disable ANSI colour output")
	flag.Parse()

	if *noColor {
		noColorMode = true
	}

	printBanner()

	// ── Load config ──────────────────────────────────────────────────────────
	cfg, err := config.Load(*cfgFile)
	if err != nil {
		fatalf("Config error: %v\n", err)
	}
	fmt.Printf(dim("  Config: %s\n\n"), *cfgFile)

	// ── Logger ───────────────────────────────────────────────────────────────
	logLevel := "warn"
	if *verbose {
		logLevel = "debug"
	}
	log := applogger.New(logLevel, "") // no file — stdout only for the test tool

	// ── --list ────────────────────────────────────────────────────────────────
	if *listF {
		printList(cfg)
		os.Exit(0)
	}

	// ── Resolve targets ───────────────────────────────────────────────────────
	targets := resolveTargets(*targetF, cfg)
	if len(targets) == 0 {
		fatalf("No backends selected. Use --target all or enable some backends in config.toml\n")
	}

	// ── Build forwarders ──────────────────────────────────────────────────────
	fwds, _ := buildForwarders(targets, cfg, log)
	if len(fwds) == 0 {
		fatalf("All selected backends failed to initialise. Check config and services.\n")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if *watchF {
		runWatch(ctx, fwds, *appName, *title, *body, *interval)
	} else {
		n := makeNotification(*appName, *title, *body, 1)
		results := sendAll(ctx, fwds, n)
		printResults(results)
		exitCode := 0
		for _, r := range results {
			if !r.ok {
				exitCode = 1
			}
		}

		// Close all backends
		for _, f := range fwds {
			f.Close()
		}
		os.Exit(exitCode)
	}
}

// ── resolveTargets ────────────────────────────────────────────────────────────

func resolveTargets(flag string, cfg *config.Config) []string {
	flag = strings.ToLower(strings.TrimSpace(flag))
	switch flag {
	case "all":
		return allTargets
	case "enabled", "":
		var out []string
		if cfg.Growl.Enabled    { out = append(out, "growl") }
		if cfg.Ntfy.Enabled     { out = append(out, "ntfy") }
		if cfg.RabbitMQ.Enabled { out = append(out, "rabbitmq") }
		if cfg.ZeroMQ.Enabled   { out = append(out, "zeromq") }
		if cfg.Redis.Enabled    { out = append(out, "redis") }
		if cfg.Database.Enabled { out = append(out, "database") }
		if cfg.Toast.Enabled    { out = append(out, "toast") }
		return out
	default:
		parts := strings.Split(flag, ",")
		var out []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
}

// ── buildForwarders ───────────────────────────────────────────────────────────

type skipEntry struct {
	name string
	err  error
}

func buildForwarders(targets []string, cfg *config.Config, log *logrus.Logger) ([]forwarder.Forwarder, []skipEntry) {
	var fwds []forwarder.Forwarder
	var skipped []skipEntry

	tryAdd := func(name string, fn func() (forwarder.Forwarder, error)) {
		fmt.Printf("  %s  Connecting to %s…", dim("…."), bold(name))
		f, err := fn()
		if err != nil {
			fmt.Printf("\r  %s  %s %s\n", yellow("SKIP"), bold(name), dim(err.Error()))
			skipped = append(skipped, skipEntry{name, err})
		} else {
			fmt.Printf("\r  %s  %s\n", green(" OK "), bold(name))
			fwds = append(fwds, f)
		}
	}

	fmt.Println(bold("  Initialising backends…"))
	for _, t := range targets {
		switch t {
		case "growl":
			// Growl Register() blocks for up to 5s (WithTimeout set in NewGrowlForwarder).
			// Run it with a channel so we show a timeout message instead of hanging.
			fmt.Printf("  %s  Connecting to %s (5s timeout)…\n", dim("…."), bold("growl"))
			type growlResult struct {
				f   forwarder.Forwarder
				err error
			}
			ch := make(chan growlResult, 1)
			go func() {
				f, err := forwarder.NewGrowlForwarder(cfg.Growl, log)
				ch <- growlResult{f, err}
			}()
			res := <-ch
			if res.err != nil {
				fmt.Printf("  %s  %s %s\n", yellow("SKIP"), bold("growl"), dim(res.err.Error()))
				skipped = append(skipped, skipEntry{"growl", res.err})
			} else {
				fmt.Printf("  %s  %s\n", green(" OK "), bold("growl"))
				fwds = append(fwds, res.f)
			}
		case "ntfy":
			// ntfy never fails at construction — it's HTTP, tested at send time
			fmt.Printf("  %s  %s\n", green(" OK "), bold("ntfy"))
			fwds = append(fwds, forwarder.NewNtfyForwarder(cfg.Ntfy, log))
		case "rabbitmq":
			tryAdd("rabbitmq", func() (forwarder.Forwarder, error) {
				return forwarder.NewRabbitMQForwarder(cfg.RabbitMQ, log)
			})
		case "zeromq":
			tryAdd("zeromq", func() (forwarder.Forwarder, error) {
				return forwarder.NewZeroMQForwarder(cfg.ZeroMQ, log)
			})
		case "redis":
			tryAdd("redis", func() (forwarder.Forwarder, error) {
				return forwarder.NewRedisForwarder(cfg.Redis, log)
			})
		case "database":
			tryAdd("database", func() (forwarder.Forwarder, error) {
				return forwarder.NewDBForwarder(cfg.Database, log)
			})
		case "toast":
			// Toast is Windows-only; on other platforms the build tag means
			// NewToastForwarder won't exist — we guard via a platform shim below.
			addToastForwarder(cfg, log, &fwds, &skipped)
		default:
			fmt.Printf("  %s  %s %s\n", yellow("UNKN"), bold(t), dim("(unknown backend name)"))
		}
	}
	fmt.Println()
	return fwds, skipped
}

// ── makeNotification ──────────────────────────────────────────────────────────

func makeNotification(appName, title, body string, seq uint32) *capture.Notification {
	return &capture.Notification{
		ID:        uuid.New().String(),
		AppName:   appName,
		Title:     title,
		Body:      body,
		Tag:       "winotif-test",
		Group:     "test",
		Sequence:  seq,
		ArrivedAt: time.Now().UTC(),
	}
}

// ── sendAll ───────────────────────────────────────────────────────────────────

func sendAll(ctx context.Context, fwds []forwarder.Forwarder, n *capture.Notification) []result {
	results := make([]result, len(fwds))
	var wg sync.WaitGroup
	for i, f := range fwds {
		wg.Add(1)
		go func(idx int, fwd forwarder.Forwarder) {
			defer wg.Done()
			start := time.Now()
			err := fwd.Forward(ctx, n)
			results[idx] = result{
				name:    fwd.Name(),
				ok:      err == nil,
				elapsed: time.Since(start),
				err:     err,
			}
		}(i, f)
	}
	wg.Wait()
	return results
}

// ── printResults ──────────────────────────────────────────────────────────────

func printResults(results []result) {
	fmt.Println(bold("  Results"))
	fmt.Println(dim("  " + strings.Repeat("─", 52)))

	pass, fail := 0, 0
	for _, r := range results {
		if r.ok {
			pass++
			fmt.Printf("  %s  %-12s %s\n",
				green(" PASS "),
				bold(r.name),
				dim(fmt.Sprintf("(%s)", r.elapsed.Round(time.Millisecond))),
			)
		} else {
			fail++
			fmt.Printf("  %s  %-12s %s\n",
				red(" FAIL "),
				bold(r.name),
				red(r.err.Error()),
			)
		}
	}

	fmt.Println(dim("  " + strings.Repeat("─", 52)))
	summary := fmt.Sprintf("  %d passed, %d failed", pass, fail)
	if fail == 0 {
		fmt.Println(green(summary))
	} else {
		fmt.Println(red(summary))
	}
	fmt.Println()
}

// ── runWatch ──────────────────────────────────────────────────────────────────

func runWatch(ctx context.Context, fwds []forwarder.Forwarder, appName, title, body string, interval time.Duration) {
	fmt.Printf(bold("  Watch mode")+" — sending every %s. Press Ctrl-C to stop.\n\n", interval)

	seq := uint32(0)
	send := func() {
		seq++
		n := makeNotification(appName, title, fmt.Sprintf("%s [#%d]", body, seq), seq)
		fmt.Printf(dim("  [%s] Sending #%d…\n"), time.Now().Format("15:04:05"), seq)
		results := sendAll(ctx, fwds, n)
		printResults(results)
	}

	send() // send immediately on start

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Println(yellow("\n  Stopped by user."))
			for _, f := range fwds {
				f.Close()
			}
			return
		case <-ticker.C:
			send()
		}
	}
}

// ── printBanner ───────────────────────────────────────────────────────────────

func printBanner() {
	fmt.Println()
	fmt.Println(bold(cyan("  ╔══════════════════════════════════════════╗")))
	fmt.Println(bold(cyan("  ║       WiNotification — Backend Tester   ║")))
	fmt.Println(bold(cyan("  ║   github.com/cumulus13/WiNotification    ║")))
	fmt.Println(bold(cyan("  ╚══════════════════════════════════════════╝")))
	fmt.Println()
}

// ── printList ────────────────────────────────────────────────────────────────

func printList(cfg *config.Config) {
	type entry struct {
		name    string
		enabled bool
		detail  string
	}
	entries := []entry{
		{"growl",    cfg.Growl.Enabled,    fmt.Sprintf("%s:%d  app=%s", cfg.Growl.Host, cfg.Growl.Port, cfg.Growl.AppName)},
		{"ntfy",     cfg.Ntfy.Enabled,     fmt.Sprintf("%s/%s  priority=%s", cfg.Ntfy.ServerURL, cfg.Ntfy.Topic, cfg.Ntfy.Priority)},
		{"rabbitmq", cfg.RabbitMQ.Enabled, fmt.Sprintf("%s  exchange=%s", cfg.RabbitMQ.URL, cfg.RabbitMQ.Exchange)},
		{"zeromq",   cfg.ZeroMQ.Enabled,   fmt.Sprintf("%s  type=%s", cfg.ZeroMQ.Bind, cfg.ZeroMQ.SocketType)},
		{"redis",    cfg.Redis.Enabled,    fmt.Sprintf("%s:%d  db=%d  ttl=%ds", cfg.Redis.Host, cfg.Redis.Port, cfg.Redis.DB, cfg.Redis.TTL)},
		{"database", cfg.Database.Enabled, fmt.Sprintf("type=%s", cfg.Database.Type)},
		{"toast",    cfg.Toast.Enabled,    fmt.Sprintf("app_id=%s  duration=%s", cfg.Toast.AppID, cfg.Toast.Duration)},
	}

	fmt.Println(bold("  Available backends"))
	fmt.Println(dim("  " + strings.Repeat("─", 60)))
	for _, e := range entries {
		var status string
		if e.enabled {
			status = green("enabled ")
		} else {
			status = dim("disabled")
		}
		fmt.Printf("  [%s]  %-10s  %s\n", status, bold(e.name), dim(e.detail))
	}
	fmt.Println()
	fmt.Println(dim("  Use --target all to test all, or --target growl,ntfy to pick specific ones."))
	fmt.Println()
}

// ── fatalf ───────────────────────────────────────────────────────────────────

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, red("  ERROR: ")+format, args...)
	os.Exit(1)
}
