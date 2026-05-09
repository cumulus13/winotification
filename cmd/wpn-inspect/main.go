//go:build windows

// wpn-inspect — dumps the real schema and sample rows from wpndatabase.db.
// Run this to discover the actual column names on your system.
//
// Usage:
//   wpn-inspect.exe
//   wpn-inspect.exe --db "C:\path\to\wpndatabase.db"
//   wpn-inspect.exe --all   (dump all rows, not just last 5)
package main

import (
	"database/sql"
	"encoding/xml"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	dbFlag := flag.String("db", "", "Path to wpndatabase.db (default: %LOCALAPPDATA%\\Microsoft\\Windows\\Notifications\\wpndatabase.db)")
	allRows := flag.Bool("all", false, "Dump all rows instead of last 5")
	flag.Parse()

	dbPath := *dbFlag
	if dbPath == "" {
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			local = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
		}
		dbPath = filepath.Join(local, "Microsoft", "Windows", "Notifications", "wpndatabase.db")
	}

	fmt.Printf("Database: %s\n\n", dbPath)
	if _, err := os.Stat(dbPath); err != nil {
		fmt.Printf("ERROR: cannot find database: %v\n", err)
		os.Exit(1)
	}

	// Try read-only first
	dsn := fmt.Sprintf("file:%s?mode=ro&immutable=1", dbPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		fmt.Printf("ERROR opening: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		fmt.Printf("ERROR pinging db: %v\n", err)
		os.Exit(1)
	}

	// ── List all tables ───────────────────────────────────────────────────
	fmt.Println("=== TABLES ===")
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		fmt.Printf("ERROR listing tables: %v\n", err)
		os.Exit(1)
	}
	var tables []string
	for rows.Next() {
		var t string
		rows.Scan(&t)
		tables = append(tables, t)
		fmt.Printf("  %s\n", t)
	}
	rows.Close()
	fmt.Println()

	// ── Schema of each table ──────────────────────────────────────────────
	for _, table := range tables {
		fmt.Printf("=== SCHEMA: %s ===\n", table)
		rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info("%s")`, table))
		if err != nil {
			fmt.Printf("  ERROR: %v\n", err)
			continue
		}
		for rows.Next() {
			var cid int
			var name, typ string
			var notnull int
			var dflt sql.NullString
			var pk int
			rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk)
			pkStr := ""
			if pk > 0 {
				pkStr = " PRIMARY KEY"
			}
			fmt.Printf("  [%d] %-25s %-15s%s\n", cid, name, typ, pkStr)
		}
		rows.Close()
		fmt.Println()
	}

	// ── Row counts ────────────────────────────────────────────────────────
	fmt.Println("=== ROW COUNTS ===")
	for _, table := range tables {
		var count int
		db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, table)).Scan(&count)
		fmt.Printf("  %-30s %d rows\n", table, count)
	}
	fmt.Println()

	// ── Sample Notification rows (last 5) ─────────────────────────────────
	fmt.Println("=== SAMPLE: Notification (last 5) ===")
	limit := "LIMIT 5"
	if *allRows {
		limit = ""
	}

	// First get actual column names
	colRows, err := db.Query(`PRAGMA table_info("Notification")`)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	var cols []string
	for colRows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		colRows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk)
		cols = append(cols, name)
	}
	colRows.Close()

	fmt.Printf("Columns: %s\n\n", strings.Join(cols, ", "))

	// Build SELECT with all columns
	colList := strings.Join(func() []string {
		quoted := make([]string, len(cols))
		for i, c := range cols {
			quoted[i] = fmt.Sprintf(`"%s"`, c)
		}
		return quoted
	}(), ", ")

	sampleRows, err := db.Query(fmt.Sprintf(
		`SELECT %s FROM "Notification" ORDER BY rowid DESC %s`, colList, limit))
	if err != nil {
		fmt.Printf("ERROR sampling Notification: %v\n", err)
	} else {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		rowNum := 0
		for sampleRows.Next() {
			rowNum++
			sampleRows.Scan(ptrs...)
			fmt.Printf("--- Row %d ---\n", rowNum)
			for i, col := range cols {
				v := vals[i]
				s := fmt.Sprintf("%v", v)
				if len(s) > 120 {
					s = s[:120] + "..."
				}
				fmt.Printf("  %-20s = %s\n", col, s)
			}
			fmt.Println()
		}
		sampleRows.Close()
	}

	// ── Sample NotificationHandler rows ──────────────────────────────────
	fmt.Println("=== SAMPLE: NotificationHandler (last 5) ===")
	handlerRows, err := db.Query(`SELECT * FROM "NotificationHandler" ORDER BY rowid DESC LIMIT 5`)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
	} else {
		hCols, _ := handlerRows.Columns()
		fmt.Printf("Columns: %s\n\n", strings.Join(hCols, ", "))
		hVals := make([]interface{}, len(hCols))
		hPtrs := make([]interface{}, len(hCols))
		for i := range hVals {
			hPtrs[i] = &hVals[i]
		}
		rowNum := 0
		for handlerRows.Next() {
			rowNum++
			handlerRows.Scan(hPtrs...)
			fmt.Printf("--- Row %d ---\n", rowNum)
			for i, col := range hCols {
				v := fmt.Sprintf("%v", hVals[i])
				if len(v) > 120 {
					v = v[:120] + "..."
				}
				fmt.Printf("  %-20s = %s\n", col, v)
			}
			fmt.Println()
		}
		handlerRows.Close()
	}

	// ── Try the JOIN query used by the engine ─────────────────────────────
	fmt.Println("=== ENGINE QUERY TEST ===")
	testQ := `
		SELECT n."Order", nh.PrimaryId, n.Payload, n.ArrivalTime
		FROM Notification n
		INNER JOIN NotificationHandler nh ON n.HandlerId = nh.RecordId
		WHERE n.Type = 'toast'
		  AND n.Payload IS NOT NULL
		ORDER BY n."Order" DESC
		LIMIT 5`
	testRows, err := db.Query(testQ)
	if err != nil {
		fmt.Printf("ENGINE QUERY FAILED: %v\n", err)
		fmt.Println(">>> Column names in the engine are WRONG — check schema above.")
	} else {
		fmt.Println("ENGINE QUERY OK — last 5 toast rows:")
		for testRows.Next() {
			var ord int64
			var primaryID string
			var payload []byte
			var arrivalTime int64
			testRows.Scan(&ord, &primaryID, &payload, &arrivalTime)

			// Parse XML to show title/body/icon
			title, body, icon := parsePayloadInspect(payload)
			fmt.Printf("\n  Order=%-8d  App=%s\n", ord, primaryID)
			fmt.Printf("  Title: %s\n", title)
			fmt.Printf("  Body:  %s\n", body)
			fmt.Printf("  Icon:  %s\n", icon)
		}
		testRows.Close()
		fmt.Println()
	}
}

// parsePayloadInspect extracts title, body, icon src from toast XML for display.
func parsePayloadInspect(payload []byte) (title, body, icon string) {
	if len(payload) == 0 {
		return "(nil)", "", ""
	}

	type xmlText struct {
		Placement string `xml:"placement,attr"`
		Value     string `xml:",chardata"`
	}
	type xmlImage struct {
		Placement string `xml:"placement,attr"`
		Src       string `xml:"src,attr"`
	}
	type xmlBinding struct {
		Texts  []xmlText  `xml:"text"`
		Images []xmlImage `xml:"image"`
	}
	type xmlVisual struct {
		Binding xmlBinding `xml:"binding"`
	}
	type xmlToast struct {
		Visual xmlVisual `xml:"visual"`
	}

	var toast xmlToast
	if err := xml.Unmarshal(payload, &toast); err != nil {
		s := string(payload)
		if len(s) > 80 {
			s = s[:80] + "..."
		}
		return s, "", ""
	}

	var lines []string
	for _, t := range toast.Visual.Binding.Texts {
		if strings.EqualFold(t.Placement, "attribution") {
			continue
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
		body = strings.Join(lines[1:], " | ")
	}
	for _, img := range toast.Visual.Binding.Images {
		if strings.EqualFold(img.Placement, "appLogoOverride") {
			icon = img.Src
			break
		}
	}
	if icon == "" {
		icon = "(none)"
	}
	return
}