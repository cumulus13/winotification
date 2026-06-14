// Package tableprint renders captured notifications as a colorized,
// auto-width table on stdout.
//
// Colors are specified as hex strings (e.g. "#FFFF00") in config.toml under
// [table.columns.<name>]. Column widths can be fixed via max_width, or left
// at 0 to auto-fill the remaining terminal width (only one column —
// "body"/MESSAGE — is intended to be auto by default).
//
// Author: Hadi Cahyadi <cumulus13@gmail.com>
package tableprint

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/cumulus13/WiNotification/internal/capture"
	"github.com/cumulus13/WiNotification/internal/config"
)

const ansiReset = "\033[0m"

// columnSpec describes a single, fixed table column and how to extract its
// value from a *capture.Notification.
type columnSpec struct {
	key          string // matches config.toml [table.columns.<key>]
	label        string
	value        func(n *capture.Notification) string
	minWidth     int
	defaultWidth int  // used when not configured and not auto
	auto         bool // true => column absorbs remaining terminal width
}

// columnOrder is fixed and deterministic — order matters for display.
var columnOrder = []columnSpec{
	{
		key: "time", label: "TIME", minWidth: 8, defaultWidth: 8,
		value: func(n *capture.Notification) string {
			return n.ArrivedAt.Local().Format("15:04:05")
		},
	},
	{
		key: "app", label: "APP", minWidth: 6, defaultWidth: 18,
		value: func(n *capture.Notification) string { return n.AppName },
	},
	{
		key: "title", label: "TITLE", minWidth: 6, defaultWidth: 24,
		value: func(n *capture.Notification) string { return n.Title },
	},
	{
		key: "body", label: "MESSAGE", minWidth: 10, defaultWidth: 0, auto: true,
		value: func(n *capture.Notification) string { return collapseWhitespace(n.Body) },
	},
	{
		key: "tag", label: "TAG", minWidth: 3, defaultWidth: 10,
		value: func(n *capture.Notification) string { return n.Tag },
	},
	{
		key: "group", label: "GROUP", minWidth: 3, defaultWidth: 10,
		value: func(n *capture.Notification) string { return n.Group },
	},
}

type resolvedColumn struct {
	spec  columnSpec
	width int
	color string // pre-rendered ANSI escape, "" if no color
}

// Printer renders Notifications as table rows on stdout. Safe for concurrent
// use — Dispatch fans out to forwarders concurrently, but the table is
// printed from a single goroutine in main_windows.go; the mutex guards
// against future callers doing otherwise.
type Printer struct {
	mu            sync.Mutex
	enabled       bool
	out           *os.File
	cols          []resolvedColumn
	borderColor   string
	headerColor   string
	headerEvery   int
	rowsSinceHead int
	headerPrinted bool
}

// NewPrinter builds a Printer from the [table] section of config.toml.
// Column widths are resolved once at startup based on the current terminal
// width; if the terminal is resized afterwards, widths are not recomputed
// (restart the app to pick up the new size — this avoids per-row syscalls).
func NewPrinter(cfg config.TableConfig) *Printer {
	noColor := cfg.NoColor || os.Getenv("NO_COLOR") != ""

	p := &Printer{
		enabled:     cfg.Enabled,
		out:         os.Stdout,
		headerEvery: cfg.HeaderEvery,
	}
	if !noColor {
		p.borderColor = hexToANSI(cfg.BorderColor)
		p.headerColor = hexToANSI(cfg.HeaderColor)
	}

	termW := terminalWidth()

	fixedTotal := 0
	autoCount := 0

	for _, spec := range columnOrder {
		w := spec.defaultWidth
		colCfg := config.TableColumnConfig{}
		if cfg.Columns != nil {
			if c, ok := cfg.Columns[spec.key]; ok {
				colCfg = c
			}
		}

		isFixedAuto := spec.auto && colCfg.MaxWidth <= 0 // truly auto column
		if colCfg.MaxWidth > 0 {
			w = colCfg.MaxWidth
		}
		if w < spec.minWidth {
			w = spec.minWidth
		}

		color := ""
		if !noColor {
			color = hexToANSI(colCfg.Color)
		}

		rc := resolvedColumn{spec: spec, width: w, color: color}
		if isFixedAuto {
			autoCount++
			rc.width = spec.minWidth // placeholder until distributed below
		} else {
			fixedTotal += w
		}
		p.cols = append(p.cols, rc)
	}

	if autoCount > 0 {
		// Layout per column: " value " plus one border char => width+3.
		// Plus one leading border char for the whole row.
		overhead := 1
		for range p.cols {
			overhead += 3
		}
		remaining := termW - fixedTotal - overhead
		share := remaining / autoCount
		const minAutoWidth = 20
		if share < minAutoWidth {
			share = minAutoWidth
		}
		for i := range p.cols {
			if p.cols[i].spec.auto {
				w := share
				if w < p.cols[i].spec.minWidth {
					w = p.cols[i].spec.minWidth
				}
				p.cols[i].width = w
			}
		}
	}

	return p
}

// Print writes one table row for n, printing (or re-printing) the header
// first when needed.
func (p *Printer) Print(n *capture.Notification) {
	if p == nil || !p.enabled || n == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.rowsSinceHead == 0 {
		if p.headerPrinted {
			fmt.Fprintln(p.out, p.footer())
		}
		fmt.Fprintln(p.out, p.header())
		p.headerPrinted = true
	}

	fmt.Fprintln(p.out, p.row(n))
	p.rowsSinceHead++

	if p.headerEvery > 0 && p.rowsSinceHead >= p.headerEvery {
		p.rowsSinceHead = 0
	}
}

// Close prints the closing table border, if a table was started. Safe to
// call even if Print was never called (no-op).
func (p *Printer) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.headerPrinted {
		fmt.Fprintln(p.out, p.footer())
		p.headerPrinted = false
	}
}

// ── rendering ──────────────────────────────────────────────────────────────

func (p *Printer) header() string {
	var sb strings.Builder

	sb.WriteString(p.borderColor)
	sb.WriteString("┌")
	for i, c := range p.cols {
		sb.WriteString(strings.Repeat("─", c.width+2))
		if i < len(p.cols)-1 {
			sb.WriteString("┬")
		}
	}
	sb.WriteString("┐")
	sb.WriteString(ansiReset)
	sb.WriteString("\n")

	sb.WriteString(p.borderColor + "│" + ansiReset)
	for _, c := range p.cols {
		label := pad(c.spec.label, c.width)
		sb.WriteString(" ")
		if p.headerColor != "" {
			sb.WriteString(p.headerColor + label + ansiReset)
		} else {
			sb.WriteString(label)
		}
		sb.WriteString(" ")
		sb.WriteString(p.borderColor + "│" + ansiReset)
	}
	sb.WriteString("\n")

	sb.WriteString(p.borderColor)
	sb.WriteString("├")
	for i, c := range p.cols {
		sb.WriteString(strings.Repeat("─", c.width+2))
		if i < len(p.cols)-1 {
			sb.WriteString("┼")
		}
	}
	sb.WriteString("┤")
	sb.WriteString(ansiReset)

	return sb.String()
}

func (p *Printer) row(n *capture.Notification) string {
	var sb strings.Builder

	sb.WriteString(p.borderColor + "│" + ansiReset)
	for _, c := range p.cols {
		val := truncate(c.spec.value(n), c.width)
		val = pad(val, c.width)

		sb.WriteString(" ")
		if c.color != "" {
			sb.WriteString(c.color + val + ansiReset)
		} else {
			sb.WriteString(val)
		}
		sb.WriteString(" ")
		sb.WriteString(p.borderColor + "│" + ansiReset)
	}

	return sb.String()
}

func (p *Printer) footer() string {
	var sb strings.Builder

	sb.WriteString(p.borderColor)
	sb.WriteString("└")
	for i, c := range p.cols {
		sb.WriteString(strings.Repeat("─", c.width+2))
		if i < len(p.cols)-1 {
			sb.WriteString("┴")
		}
	}
	sb.WriteString("┘")
	sb.WriteString(ansiReset)

	return sb.String()
}

// ── helpers ───────────────────────────────────────────────────────────────

// hexToANSI converts "#RRGGBB" (or "RRGGBB") into a 24-bit ANSI foreground
// escape sequence. Returns "" for empty/invalid input — callers treat that
// as "no color applied".
func hexToANSI(hex string) string {
	hex = strings.TrimSpace(hex)
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return ""
	}
	r, errR := strconv.ParseUint(hex[0:2], 16, 8)
	g, errG := strconv.ParseUint(hex[2:4], 16, 8)
	b, errB := strconv.ParseUint(hex[4:6], 16, 8)
	if errR != nil || errG != nil || errB != nil {
		return ""
	}
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
}

// collapseWhitespace flattens multi-line notification bodies into a single
// line so they fit cleanly in one table row.
func collapseWhitespace(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " | ")
	s = strings.ReplaceAll(s, "\n", " | ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.TrimSpace(s)
}

// truncate shortens s to at most width runes, appending "…" if cut.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(r[:width-1]) + "…"
}

// pad right-pads s with spaces to width runes (no-op if already >= width).
func pad(s string, width int) string {
	l := len([]rune(s))
	if l >= width {
		return s
	}
	return s + strings.Repeat(" ", width-l)
}

// defaultTerminalWidth is the platform-independent fallback used when the
// real terminal width cannot be determined (e.g. output redirected to a
// file/pipe, or COLUMNS unset).
func defaultTerminalWidth() int {
	if v := os.Getenv("COLUMNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 120
}