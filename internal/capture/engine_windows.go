//go:build windows

// Package capture reads Windows notifications from the WPN (Windows Push
// Notification) SQLite database — the only approach that reliably works from
// a plain unpackaged Win32 exe without any special permissions.
//
// Database location (per-user):
//   %LOCALAPPDATA%\Microsoft\Windows\Notifications\wpndatabase.db
//
// Schema (confirmed from forensic research and direct observation):
//   Notification table:
//     Id          INTEGER  — unique row ID, monotonically increasing
//     HandlerId   INTEGER  — FK → NotificationHandler.RecordId
//     Payload     BLOB     — XML content of the notification
//     ArrivalTime INTEGER  — Windows FILETIME (100ns ticks since 1601-01-01)
//     ExpiryTime  INTEGER  — expiry in same format
//     Type        INTEGER  — notification type
//
//   NotificationHandler table:
//     RecordId    INTEGER  — PK
//     PrimaryId   TEXT     — app identifier (e.g. "Microsoft.Teams_...")
//
// Strategy:
//   Poll the DB every intervalMs. Track the highest Id seen.
//   On each poll, SELECT rows with Id > lastSeen, parse XML payload,
//   emit as Notification. No hooks, no COM, no UWP, no permissions needed.
//
// The DB is opened with mode=ro&_journal_mode=WAL so we don't interfere with
// the live WPN service writing to it.
//
// Author:   Hadi Cahyadi <cumulus13@gmail.com>
// Homepage: github.com/cumulus13/WiNotification
package capture

import (
	"context"
	"database/sql"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/sirupsen/logrus"
)

// ── WPN database path ─────────────────────────────────────────────────────────

func wpnDBPath() string {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		// Fallback: construct from USERPROFILE
		local = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
	}
	return filepath.Join(local, "Microsoft", "Windows", "Notifications", "wpndatabase.db")
}

// ── Toast XML parsing ─────────────────────────────────────────────────────────

// toastXML mirrors the structure of Windows toast notification XML payloads.
type toastXML struct {
	Visual struct {
		Binding struct {
			Texts []struct {
				Value string `xml:",chardata"`
			} `xml:"text"`
		} `xml:"binding"`
	} `xml:"visual"`
}

// parsePayload extracts title and body from a Windows notification XML payload.
// The payload is a BLOB that may be raw bytes or a UTF-8 XML string.
func parsePayload(payload []byte) (title, body string) {
	if len(payload) == 0 {
		return "", ""
	}

	var toast toastXML
	if err := xml.Unmarshal(payload, &toast); err != nil {
		// Payload may not be valid XML (tile/badge types) — return raw trimmed.
		s := strings.TrimSpace(string(payload))
		if len(s) > 200 {
			s = s[:200]
		}
		return s, ""
	}

	texts := toast.Visual.Binding.Texts
	if len(texts) == 0 {
		return "", ""
	}
	title = strings.TrimSpace(texts[0].Value)
	var parts []string
	for _, t := range texts[1:] {
		v := strings.TrimSpace(t.Value)
		if v != "" {
			parts = append(parts, v)
		}
	}
	body = strings.Join(parts, "\n")
	return
}

// filetimeToTime converts a Windows FILETIME integer to time.Time.
// Formula: unix_seconds = (filetime / 10_000_000) - 11_644_473_600
func filetimeToTime(ft int64) time.Time {
	if ft == 0 {
		return time.Now().UTC()
	}
	unixSec := (ft / 10_000_000) - 11_644_473_600
	return time.Unix(unixSec, 0).UTC()
}

// ── Engine ────────────────────────────────────────────────────────────────────

// Engine polls the WPN SQLite database for new notifications.
type Engine struct {
	dbPath     string
	intervalMs int
	filterApps map[string]struct{}
	ignoreApps map[string]struct{}
	logger     *logrus.Logger
	out        chan<- *Notification
	lastID     int64 // highest Notification.Id seen so far
}

func NewEngine(
	intervalMs int,
	filterApps []string,
	ignoreApps []string,
	out chan<- *Notification,
	log *logrus.Logger,
) *Engine {
	fa := make(map[string]struct{}, len(filterApps))
	for _, a := range filterApps {
		fa[strings.ToLower(a)] = struct{}{}
	}
	ia := make(map[string]struct{}, len(ignoreApps))
	for _, a := range ignoreApps {
		ia[strings.ToLower(a)] = struct{}{}
	}
	return &Engine{
		dbPath:     wpnDBPath(),
		intervalMs: intervalMs,
		filterApps: fa,
		ignoreApps: ia,
		logger:     log,
		out:        out,
	}
}

// Run polls the WPN database until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) error {
	if _, err := os.Stat(e.dbPath); err != nil {
		return fmt.Errorf("WPN database not found at %s: %w", e.dbPath, err)
	}

	// Seed lastID so we only forward notifications that arrive AFTER startup.
	// Open a fresh connection just for seeding, then close it.
	if err := e.seedLastID(ctx); err != nil {
		e.logger.WithError(err).Warn("Could not seed lastID — will emit all existing notifications on first poll")
	}

	e.logger.Infof("Capture engine started — polling %s every %dms (lastID=%d)", e.dbPath, e.intervalMs, e.lastID)

	ticker := time.NewTicker(time.Duration(e.intervalMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			e.logger.Info("Capture engine stopped")
			return nil
		case <-ticker.C:
			if err := e.poll(ctx); err != nil {
				e.logger.WithError(err).Warn("Poll error")
			}
		}
	}
}

// openDB opens a fresh read-only connection to the WPN database.
// A new connection is opened on every poll so SQLite always reads the current
// file from disk — immutable=1 or a persistent connection would cache the
// file at open time and miss new rows written by the WPN service.
func (e *Engine) openDB() (*sql.DB, error) {
	// mode=ro  — read-only, never writes
	// cache=shared — share page cache across connections
	// No immutable=1 — we NEED to see changes written by Windows
	// No _journal_mode — read-only connections cannot change journal mode
	dsn := fmt.Sprintf("file:%s?mode=ro&cache=shared", e.dbPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// seedLastID sets lastID to the current maximum rowid so we skip history.
func (e *Engine) seedLastID(ctx context.Context) error {
	db, err := e.openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	var maxID sql.NullInt64
	err = db.QueryRowContext(ctx, `SELECT MAX(Id) FROM Notification`).Scan(&maxID)
	if err != nil {
		return err
	}
	if maxID.Valid {
		e.lastID = maxID.Int64
	}
	e.logger.Infof("Seeded lastID=%d (existing notifications will be skipped)", e.lastID)
	return nil
}

// poll opens a fresh DB connection, queries for rows newer than lastID, emits them.
func (e *Engine) poll(ctx context.Context) error {
	db, err := e.openDB()
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	const query = `
		SELECT n.Id, nh.PrimaryId, n.Payload, n.ArrivalTime
		FROM Notification n
		INNER JOIN NotificationHandler nh ON n.HandlerId = nh.RecordId
		WHERE n.Id > ?
		ORDER BY n.Id ASC`

	rows, err := db.QueryContext(ctx, query, e.lastID)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id          int64
			primaryID   string
			payload     []byte
			arrivalTime int64
		)
		if err := rows.Scan(&id, &primaryID, &payload, &arrivalTime); err != nil {
			e.logger.WithError(err).Warn("Row scan error")
			continue
		}

		if id > e.lastID {
			e.lastID = id
		}

		title, body := parsePayload(payload)
		if title == "" {
			continue
		}

		n := &Notification{
			ID:        uuid.New().String(),
			AppName:   primaryID,
			Title:     title,
			Body:      body,
			ArrivedAt: filetimeToTime(arrivalTime),
		}

		if !e.shouldForward(n) {
			continue
		}

		e.logger.Infof("Captured [%s] %q — %q", n.AppName, n.Title, n.Body)
		select {
		case e.out <- n:
		default:
			e.logger.Warn("Channel full, dropping: ", n.Title)
		}
	}

	return rows.Err()
}

// shouldForward applies app filter/ignore rules.
func (e *Engine) shouldForward(n *Notification) bool {
	lower := strings.ToLower(n.AppName)
	if _, ignored := e.ignoreApps[lower]; ignored {
		return false
	}
	if len(e.filterApps) > 0 {
		_, ok := e.filterApps[lower]
		return ok
	}
	return true
}

// RequestAccess is a no-op — reading wpndatabase.db needs no special access.
func RequestAccess(log *logrus.Logger) error {
	log.Info("WPN database engine needs no special access grant.")
	return nil
}
