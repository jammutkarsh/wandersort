// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/tui"
	"github.com/spf13/cobra"
	_ "modernc.org/sqlite"
)

// terminalStatus reports whether status is an end-state; sessions still
// in-flight (STARTED/SCANNING/HASHING/...) have partial counts.
func terminalStatus(status string) bool {
	switch status {
	case db.StatusCompleted, db.StatusFailed, db.StatusCancelled:
		return true
	default:
		return false
	}
}

// sessionReport summarizes one scan session. Duplicates are scoped to the
// session's own files, since two sessions over different roots can have
// entirely different duplicate pictures.
type sessionReport struct {
	SessionID       string `json:"sessionId"`
	Status          string `json:"status"`
	StartedAt       string `json:"startedAt"`
	FilesScanned    int64  `json:"filesScanned"`
	FilesHashed     int64  `json:"filesHashed"`
	FilesDuplicated int64  `json:"filesDuplicated"`
}

var (
	labelStyle       = tui.DimText
	valueStyle       = tui.Text.Bold(true)
	titleStyle       = tui.Title.MarginBottom(1)
	tableBorderStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(tui.Subtle).Padding(0, 1)
)

func (a *app) newReportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Show a summary of the last scan",
		Long: `Prints a report of scanned files, hashed files, and detected duplicates.
Use --vertical (-x, psql-style expanded display) on narrow terminals where the
table would wrap.`,
		Example: `wandersort report
wandersort report -o ~/wandersort-out
wandersort report -x`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runReport()
		},
	}

	cmd.Flags().BoolP(flagVertical, "x", false, "Show each session as expanded label:value pairs instead of a table")
	return cmd
}

func (a *app) runReport() error {
	if _, err := os.Stat(a.Config.AppDBPath); os.IsNotExist(err) {
		return fmt.Errorf("no database found — nothing to report on")
	}

	ctx := context.Background()

	results, err := a.generateReport(ctx, a.Config.AppDBPath)
	if err != nil {
		return fmt.Errorf("report: %w", err)
	}
	if len(results) == 0 {
		return fmt.Errorf("no scan data found — run `wandersort scan` first")
	}

	if v.GetBool(flagVertical) {
		a.printReportVertical(results)
	} else {
		a.printReport(results)
	}
	return nil
}

func (a *app) generateReport(ctx context.Context, dbPath string) ([]sessionReport, error) {
	// busy_timeout lets sqlite wait out a concurrent scan/serve writer instead
	// of failing outright with SQLITE_BUSY on this read-only connection.
	dsn := fmt.Sprintf("file:%s?mode=ro&_journal=OFF&_pragma=busy_timeout(5000)", dbPath)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db read-only: %w", err)
	}
	defer sqlDB.Close()

	rows, err := sqlDB.QueryContext(ctx, `SELECT id, status, started_at FROM scan_sessions ORDER BY started_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list scan_sessions: %w", err)
	}
	defer rows.Close()

	var sessions []sessionReport
	for rows.Next() {
		var s sessionReport
		if err := rows.Scan(&s.SessionID, &s.Status, &s.StartedAt); err != nil {
			return nil, fmt.Errorf("scan scan_sessions row: %w", err)
		}
		sessions = append(sessions, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scan_sessions: %w", err)
	}

	for i := range sessions {
		if err := sqlDB.QueryRowContext(
			ctx, `SELECT COUNT(*) FROM live_files WHERE scan_session_id = ?`, sessions[i].SessionID,
		).Scan(&sessions[i].FilesScanned); err != nil {
			return nil, fmt.Errorf("count file_registry for session %s: %w", sessions[i].SessionID, err)
		}

		if err := sqlDB.QueryRowContext(
			ctx, `
			SELECT COUNT(*) FROM file_metadata fm
			JOIN live_files fr ON fr.id = fm.file_id
			WHERE fr.scan_session_id = ?`, sessions[i].SessionID,
		).Scan(&sessions[i].FilesHashed); err != nil {
			return nil, fmt.Errorf("count file_metadata for session %s: %w", sessions[i].SessionID, err)
		}

		if err := sqlDB.QueryRowContext(
			ctx, `
			SELECT COUNT(*) FROM file_metadata fm
			JOIN live_files fr ON fr.id = fm.file_id
			WHERE fr.scan_session_id = ? AND fm.file_hash IN (
				SELECT fm2.file_hash FROM file_metadata fm2
				JOIN live_files fr2 ON fr2.id = fm2.file_id
				WHERE fr2.scan_session_id = ?
				GROUP BY fm2.file_hash HAVING COUNT(*) > 1
			)`, sessions[i].SessionID, sessions[i].SessionID,
		).Scan(&sessions[i].FilesDuplicated); err != nil {
			return nil, fmt.Errorf("count duplicates for session %s: %w", sessions[i].SessionID, err)
		}
	}

	return sessions, nil
}

func (a *app) printReport(sessions []sessionReport) {
	fmt.Println(titleStyle.Render("WanderSort Report"))

	if !terminalStatus(sessions[0].Status) {
		fmt.Println(tui.Attn.Render(fmt.Sprintf(
			"Session %s is still %s — its counts below are partial.", sessions[0].SessionID, sessions[0].Status,
		)))
		fmt.Println()
	}

	rowFmt := "%-36s  %-19s  %-11s  %8s  %8s  %10s"
	header := labelStyle.Render(fmt.Sprintf(rowFmt,
		"SESSION", "STARTED", "STATUS", "SCANNED", "HASHED", "DUPLICATES"))

	var body strings.Builder
	body.WriteString(header)
	for _, s := range sessions {
		row := fmt.Sprintf(rowFmt,
			s.SessionID, humanTime(s.StartedAt), s.Status,
			strconv.FormatInt(s.FilesScanned, 10), strconv.FormatInt(s.FilesHashed, 10), strconv.FormatInt(s.FilesDuplicated, 10))
		body.WriteString("\n")
		body.WriteString(valueStyle.Render(row))
	}

	fmt.Println(tableBorderStyle.Render(body.String()))
}

// printReportVertical renders each session as expanded label:value pairs
// (psql \x style) instead of a wide table, for narrow terminals.
func (a *app) printReportVertical(sessions []sessionReport) {
	fmt.Println(titleStyle.Render("WanderSort Report"))

	if !terminalStatus(sessions[0].Status) {
		fmt.Println(tui.Attn.Render(fmt.Sprintf(
			"Session %s is still %s — its counts below are partial.", sessions[0].SessionID, sessions[0].Status,
		)))
		fmt.Println()
	}

	for i, s := range sessions {
		fmt.Println(labelStyle.Render(fmt.Sprintf("-[ session %d ]-", i+1)))
		printField("Session ID", s.SessionID)
		printField("Started", humanTime(s.StartedAt))
		printField("Status", s.Status)
		printField("Scanned", strconv.FormatInt(s.FilesScanned, 10))
		printField("Hashed", strconv.FormatInt(s.FilesHashed, 10))
		printField("Duplicates", strconv.FormatInt(s.FilesDuplicated, 10))
		fmt.Println()
	}
}

func printField(label, value string) {
	fmt.Println(labelStyle.Render(fmt.Sprintf("%-10s | ", label)) + valueStyle.Render(value))
}

// humanTime renders a stored RFC3339 timestamp in local, human-readable form.
// Falls back to the raw value if it doesn't parse.
func humanTime(raw string) string {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return raw
	}
	return t.Local().Format("2006-01-02 15:04:05")
}
