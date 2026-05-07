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
		SELECT n.Id, nh.PrimaryId, n.ArrivalTime
		FROM Notification n
		INNER JOIN NotificationHandler nh ON n.HandlerId = nh.RecordId
		ORDER BY n.Id DESC
		LIMIT 3`
	testRows, err := db.Query(testQ)
	if err != nil {
		fmt.Printf("ENGINE QUERY FAILED: %v\n", err)
		fmt.Println()
		fmt.Println(">>> This means the column names in the engine are WRONG.")
		fmt.Println(">>> Use the schema output above to find the correct names.")
	} else {
		fmt.Println("ENGINE QUERY OK — last 3 rows:")
		for testRows.Next() {
			var id int64
			var primaryID string
			var arrivalTime int64
			testRows.Scan(&id, &primaryID, &arrivalTime)
			unixSec := (arrivalTime / 10_000_000) - 11_644_473_600
			fmt.Printf("  Id=%-6d  App=%-40s  ArrivalTime=%d\n", id, primaryID, unixSec)
		}
		testRows.Close()
	}
}
