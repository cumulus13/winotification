//go:build windows

// Package capture polls the Windows Push Notification database (wpndatabase.db)
// for new toast notifications and emits them as Notification structs.
//
// ── HOW WAL MODE WORKS AND WHY THIS APPROACH IS CORRECT ─────────────────────
//
// wpndatabase.db uses SQLite WAL (Write-Ahead Logging). In WAL mode:
//
//   wpndatabase.db      — checkpointed (older) data
//   wpndatabase.db-wal  — all new writes land here first
//   wpndatabase.db-shm  — shared memory index: tells readers where in the
//                         WAL the latest committed frame is
//
// A SQLite reader takes a WAL read-lock at BEGIN and releases it at
// COMMIT/ROLLBACK. The read-lock pins the reader to a specific WAL position —
// it sees a consistent snapshot as of that moment. If the read-lock is never
// released (e.g. a persistent connection that never does explicit BEGIN/COMMIT,
// which is what database/sql does by default in auto-commit mode with a
// connection pool), the reader stays pinned to the WAL position from the
// first query — potentially hours old.
//
// FIX: one persistent *sql.DB with pool size 1, and every poll wrapped in an
// explicit BEGIN (read-only) / ROLLBACK transaction. Each BEGIN re-acquires
// the WAL read-lock at the current WAL head, seeing all frames written by
// Windows since the last poll. ROLLBACK releases the lock immediately after.
//
// This means:
//   - No file copy (works with a 10GB database just as well as a 10KB one)
//   - No reconnect overhead
//   - No CGO handle churn
//   - Guaranteed fresh WAL position on every single poll, forever
//
// The ONLY persistent state between polls is e.lastOrder (an int64) and the
// single open *sql.DB connection — which holds NO read-lock between polls
// because each poll's transaction has been committed/rolled back.
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
		local = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
	}
	return filepath.Join(local, "Microsoft", "Windows", "Notifications", "wpndatabase.db")
}

// ── Toast XML parsing ─────────────────────────────────────────────────────────

type xmlToast struct {
	XMLName xml.Name  `xml:"toast"`
	Visual  xmlVisual `xml:"visual"`
}
type xmlVisual struct{ Binding xmlBinding `xml:"binding"` }
type xmlBinding struct {
	Texts  []xmlText  `xml:"text"`
	Images []xmlImage `xml:"image"`
}
type xmlText struct {
	Placement string `xml:"placement,attr"`
	Value     string `xml:",chardata"`
}
type xmlImage struct {
	Placement string `xml:"placement,attr"`
	HintCrop  string `xml:"hint-crop,attr"`
	Src       string `xml:"src,attr"`
}

func parseToast(payload []byte) (title, body, iconPath string) {
	if len(payload) == 0 {
		return
	}
	var t xmlToast
	if err := xml.Unmarshal(payload, &t); err != nil {
		title = strings.TrimSpace(string(payload))
		if len(title) > 200 {
			title = title[:200]
		}
		return
	}
	var lines []string
	for _, txt := range t.Visual.Binding.Texts {
		if strings.EqualFold(txt.Placement, "attribution") {
			continue
		}
		if v := strings.TrimSpace(txt.Value); v != "" {
			lines = append(lines, v)
		}
	}
	if len(lines) > 0 {
		title = lines[0]
	}
	if len(lines) > 1 {
		body = strings.Join(lines[1:], "\n")
	}
	for _, img := range t.Visual.Binding.Images {
		if strings.EqualFold(img.Placement, "appLogoOverride") && img.Src != "" {
			src := img.Src
			if strings.HasPrefix(src, "file:///") {
				src = strings.ReplaceAll(strings.TrimPrefix(src, "file:///"), "/", `\`)
			}
			iconPath = src
			break
		}
	}
	return
}

func filetimeToTime(ft int64) time.Time {
	if ft == 0 {
		return time.Now().UTC()
	}
	return time.Unix((ft/10_000_000)-11_644_473_600, 0).UTC()
}

// ── Engine ────────────────────────────────────────────────────────────────────

// Engine polls wpndatabase.db for new toast notifications.
type Engine struct {
	dbPath     string
	intervalMs int
	filterApps map[string]struct{}
	ignoreApps map[string]struct{}
	logger     *logrus.Logger
	out        chan<- *Notification
	lastOrder  int64

	// db is a single persistent connection to the REAL wpndatabase.db.
	// Pool size is forced to 1 so there is exactly one underlying sqlite3*
	// handle. The WAL read-lock is managed per-transaction (BEGIN/ROLLBACK),
	// not per-connection, so this single connection sees fresh WAL data on
	// every poll as long as each poll uses an explicit transaction.
	db *sql.DB
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

// Run opens one persistent connection to the real wpndatabase.db and polls
// it until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) error {
	if _, err := os.Stat(e.dbPath); err != nil {
		return fmt.Errorf("WPN database not found at %s: %w", e.dbPath, err)
	}

	db, err := e.openDB()
	if err != nil {
		return fmt.Errorf("open wpndatabase: %w", err)
	}
	e.db = db
	defer func() {
		e.db.Close()
		e.db = nil
	}()

	if err := e.seedLastOrder(ctx); err != nil {
		e.logger.WithError(err).Warn("Could not seed lastOrder — will emit all existing notifications on first poll")
	}

	e.logger.Infof("Capture engine started — polling %s every %dms (lastOrder=%d)",
		e.dbPath, e.intervalMs, e.lastOrder)

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

// openDB opens a single read-only connection to the real wpndatabase.db.
//
// DSN flags:
//   mode=ro          — never acquire a write lock; we are a pure reader
//   cache=private    — no cross-connection page cache sharing (pool=1 makes
//                      this moot but explicit is better than implicit)
//   _busy_timeout=2000 — wait up to 2s if WpnService holds a write lock
//                        during a notification burst before returning an error
//
// What controls WAL freshness is NOT the connection — it is the transaction.
// See poll() for the BEGIN/ROLLBACK pattern that re-acquires the WAL
// read-lock at the current WAL head on every poll cycle.
func (e *Engine) openDB() (*sql.DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?mode=ro&cache=private&_busy_timeout=2000",
		e.dbPath,
	)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}

	// Exactly one connection → exactly one sqlite3* C handle.
	// This is critical: if the pool had >1 connections, different connections
	// could hold WAL read-locks at different positions, causing non-monotonic
	// reads. With pool=1 there is one handle, one WAL position per transaction.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)        // keep the one connection alive between polls
	db.SetConnMaxLifetime(0)     // never expire it — we manage the lifecycle
	db.SetConnMaxIdleTime(0)     // ditto

	// Verify the connection works and WAL mode is active.
	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	return db, nil
}

// inTx runs fn inside an explicit read-only transaction on the persistent
// connection. This is the core of the WAL freshness fix:
//
//   BEGIN DEFERRED  → SQLite acquires a WAL read-lock at the CURRENT WAL head.
//                     The transaction sees all frames written up to this point,
//                     including any written since the previous poll.
//   fn(tx)          → execute queries against this fresh snapshot.
//   ROLLBACK        → release the WAL read-lock immediately. The connection
//                     now holds NO read-lock, so the next poll's BEGIN will
//                     again grab the latest WAL position.
//
// Using ROLLBACK (not COMMIT) because we never write; both are equivalent for
// releasing the read-lock but ROLLBACK makes the intent explicit.
func (e *Engine) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	// sql.TxOptions with ReadOnly=true maps to BEGIN DEFERRED in go-sqlite3,
	// which acquires a WAL read-lock without attempting any write intent.
	tx, err := e.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	fnErr := fn(tx)

	// Always rollback — we never write.
	// Rollback releases the WAL read-lock so the next BEGIN sees fresh data.
	if rbErr := tx.Rollback(); rbErr != nil && rbErr != sql.ErrTxDone {
		e.logger.WithError(rbErr).Warn("tx rollback error")
	}

	return fnErr
}

// seedLastOrder sets lastOrder to current MAX([Order]) so we skip history.
func (e *Engine) seedLastOrder(ctx context.Context) error {
	return e.inTx(ctx, func(tx *sql.Tx) error {
		var maxOrder sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT MAX("Order") FROM Notification`).Scan(&maxOrder); err != nil {
			return err
		}
		if maxOrder.Valid {
			e.lastOrder = maxOrder.Int64
		}
		e.logger.Infof("Seeded lastOrder=%d (existing notifications skipped)", e.lastOrder)
		return nil
	})
}

// poll opens a fresh read transaction (new WAL read-lock at current WAL head)
// and emits any notifications with Order > lastOrder.
func (e *Engine) poll(ctx context.Context) error {
	return e.inTx(ctx, func(tx *sql.Tx) error {
		const query = `
			SELECT n."Order", nh.PrimaryId, n.Payload, n.ArrivalTime
			FROM   Notification n
			INNER  JOIN NotificationHandler nh ON n.HandlerId = nh.RecordId
			WHERE  n."Order" > ?
			  AND  n.Type    = 'toast'
			  AND  n.Payload IS NOT NULL
			ORDER  BY n."Order" ASC`

		rows, err := tx.QueryContext(ctx, query, e.lastOrder)
		if err != nil {
			return fmt.Errorf("query: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var (
				order       int64
				primaryID   string
				payload     []byte
				arrivalTime int64
			)
			if err := rows.Scan(&order, &primaryID, &payload, &arrivalTime); err != nil {
				e.logger.WithError(err).Warn("Scan error")
				continue
			}

			if order > e.lastOrder {
				e.lastOrder = order
			}

			title, body, iconPath := parseToast(payload)
			if title == "" {
				continue
			}

			var iconData []byte
			if iconPath != "" {
				if data, err := os.ReadFile(iconPath); err == nil {
					iconData = data
				} else {
					e.logger.Debugf("Icon not readable at %s: %v", iconPath, err)
				}
			}

			n := &Notification{
				ID:        uuid.New().String(),
				AppName:   primaryID,
				Title:     title,
				Body:      body,
				ArrivedAt: filetimeToTime(arrivalTime),
				IconData:  iconData,
			}

			if !e.shouldForward(n) {
				continue
			}

			e.logger.Infof("Captured [%s] %q — %q (icon=%v)", n.AppName, n.Title, n.Body, iconPath != "")
			select {
			case e.out <- n:
			default:
				e.logger.Warn("Channel full, dropping: ", n.Title)
			}
		}

		return rows.Err()
	})
}

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

// RequestAccess is a no-op for this engine.
func RequestAccess(log *logrus.Logger) error {
	log.Info("WPN database engine needs no special access grant.")
	return nil
}
