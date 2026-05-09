//go:build windows

// Package capture polls the Windows Push Notification database (wpndatabase.db)
// for new toast notifications and emits them as Notification structs.
//
// Schema (confirmed from wpn-inspect output on your system):
//
//   Notification table:
//     [0] Order       INTEGER  PRIMARY KEY  ← monotonic rowid — use this for tracking
//     [1] Id          INTEGER               ← NOT the PK, do not use for WHERE >
//     [2] HandlerId   INTEGER               ← FK → NotificationHandler.RecordId
//     [3] Type        TEXT                  ← "toast" has payload; "toastCondensed" = NULL payload
//     [4] Payload     BLOB                  ← XML, NULL for toastCondensed rows
//     [5] ArrivalTime INT64                 ← Windows FILETIME
//
//   NotificationHandler table:
//     RecordId   INTEGER PRIMARY KEY
//     PrimaryId  TEXT    ← app name (e.g. "DTOP", "MailClient", "go-gitdate")
//
// Toast XML structure (from real data):
//   <toast>
//     <visual>
//       <binding template="ToastGeneric">
//         <text>Title line</text>
//         <text>Body line</text>
//         <text placement="attribution">Attribution — SKIP</text>
//         <image placement="appLogoOverride" src="file:///C:\path\to\icon.png"/>
//       </binding>
//     </visual>
//   </toast>
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

// xmlToast mirrors the Windows toast XML structure seen in real wpndatabase rows.
type xmlToast struct {
	XMLName xml.Name    `xml:"toast"`
	Visual  xmlVisual   `xml:"visual"`
}

type xmlVisual struct {
	Binding xmlBinding `xml:"binding"`
}

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

// parseToast extracts title, body, and icon path from a Windows toast XML payload.
//
// Text rules (from real data):
//   - Texts without placement="attribution" in order: first=title, second=body
//   - Texts with placement="attribution" are skipped (e.g. email account name)
//
// Icon rules (from real data):
//   - <image placement="appLogoOverride" src="file:///C:\path\to\icon.png"/>
//   - src may be a file:// URI or http:// URL
//   - Strip "file:///" prefix to get local path
func parseToast(payload []byte) (title, body, iconPath string) {
	if len(payload) == 0 {
		return
	}

	var toast xmlToast
	if err := xml.Unmarshal(payload, &toast); err != nil {
		// Not valid XML — treat raw bytes as title
		title = strings.TrimSpace(string(payload))
		if len(title) > 200 {
			title = title[:200]
		}
		return
	}

	// Extract non-attribution text lines in order
	var lines []string
	for _, t := range toast.Visual.Binding.Texts {
		if strings.EqualFold(t.Placement, "attribution") {
			continue // skip attribution text
		}
		v := strings.TrimSpace(t.Value)
		if v != "" {
			lines = append(lines, v)
		}
	}

	if len(lines) > 0 {
		title = lines[0]
	}
	if len(lines) > 1 {
		body = strings.Join(lines[1:], "\n")
	}

	// Extract icon from appLogoOverride image
	for _, img := range toast.Visual.Binding.Images {
		if strings.EqualFold(img.Placement, "appLogoOverride") && img.Src != "" {
			src := img.Src
			// Strip file:/// prefix → local Windows path
			if strings.HasPrefix(src, "file:///") {
				src = strings.TrimPrefix(src, "file:///")
				src = strings.ReplaceAll(src, "/", `\`)
			}
			iconPath = src
			break
		}
	}

	return
}

// filetimeToTime converts a Windows FILETIME (100ns ticks since 1601-01-01) to time.Time.
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
	lastOrder  int64 // tracks highest `Order` value seen — Order is the true PK
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

// openDB opens a fresh read-only connection. Fresh per poll so SQLite
// always reads current file state — persistent connection caches stale data.
func (e *Engine) openDB() (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&cache=shared", e.dbPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// seedLastOrder sets lastOrder to current MAX([Order]) so we skip history.
func (e *Engine) seedLastOrder(ctx context.Context) error {
	db, err := e.openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	var maxOrder sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX("Order") FROM Notification`).Scan(&maxOrder); err != nil {
		return err
	}
	if maxOrder.Valid {
		e.lastOrder = maxOrder.Int64
	}
	e.logger.Infof("Seeded lastOrder=%d (existing notifications skipped)", e.lastOrder)
	return nil
}

// poll queries for rows with Order > lastOrder, type='toast', non-NULL payload.
func (e *Engine) poll(ctx context.Context) error {
	db, err := e.openDB()
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	// Only select toast rows (not toastCondensed) with non-NULL Payload.
	// Use `Order` (the true PRIMARY KEY) for tracking, not `Id`.
	const query = `
		SELECT n."Order", nh.PrimaryId, n.Payload, n.ArrivalTime
		FROM Notification n
		INNER JOIN NotificationHandler nh ON n.HandlerId = nh.RecordId
		WHERE n."Order" > ?
		  AND n.Type = 'toast'
		  AND n.Payload IS NOT NULL
		ORDER BY n."Order" ASC`

	rows, err := db.QueryContext(ctx, query, e.lastOrder)
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

		// Load icon bytes from local file path if present
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
