package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Temikus/denkeeper/internal/audit"
)

// Exit codes for `denkeeper audit verify`: 0 verified (or empty), 1 a break
// or an error, 2 nothing covered (no row has a hash).
const exitNotCovered = 2

// exitError carries a process exit code other than 1 up to main.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

// exitCode is the process exit code for an error returned by a command.
func exitCode(err error) int {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return 1
}

func newAuditCmd() *cobra.Command {
	auditCmd := &cobra.Command{
		Use:   "audit",
		Short: "Inspect the audit log",
	}
	auditCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file path (default: ~/.denkeeper/denkeeper.toml)")

	verifyCmd := &cobra.Command{
		Use:   "verify",
		Short: "Check the audit log's hash chain",
		Long: "Walks the audit log in id order and checks every event against its hash and the hash of the event\n" +
			"before it. Exits non-zero at the first event that was changed, removed, added or reordered.\n" +
			"Read-only, and safe while denkeeper serve is running. It cannot see removed newest events or a\n" +
			"full rewrite of the file: keep a copy of the Head line somewhere else to catch those.\n\n" +
			"Exit codes: 0 verified (or no events), 1 broken or an error, 2 no event has a hash, so nothing\n" +
			"could be verified.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuditVerify(cmd.Context(), os.Stdout, os.Stderr, cfgFile)
		},
	}
	auditCmd.AddCommand(verifyCmd)
	return auditCmd
}

func runAuditVerify(ctx context.Context, w, progress io.Writer, cfgPath string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	path := filepath.Join(filepath.Dir(resolveDBPath(cfgPath)), "audit.db")
	// Stat first: the writable opener would create an empty database.
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no audit log at %s (is [audit] enabled?): %w", path, err)
	}
	store, err := audit.OpenSQLiteStoreReadOnly(path)
	if err != nil {
		return fmt.Errorf("opening audit log at %s: %w", path, err)
	}
	defer func() { _ = store.Close() }()

	_, _ = fmt.Fprintf(progress, "Verifying audit log %s...\n", path)
	rep, err := store.VerifyChain(ctx)
	if err != nil {
		return fmt.Errorf("verifying audit log at %s: %w", path, err)
	}
	writeChainReport(w, path, rep)
	switch {
	case rep.Break != nil:
		return fmt.Errorf("audit log failed verification at event %d", rep.Break.ID)
	case rep.Start == audit.ChainStartNone:
		return &exitError{code: exitNotCovered, err: errors.New("audit log has no hashed events, so nothing was verified")}
	}
	return nil
}

// writeChainReport prints a ChainReport. It is a pure function of the report.
func writeChainReport(w io.Writer, path string, r *audit.ChainReport) {
	p := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format+"\n", args...) }
	p("Audit log: %s", path)
	switch {
	case r.Break != nil:
		writeBreak(p, r)
	case r.Start == audit.ChainStartEmpty:
		p("No audit events. Nothing to verify.")
	case r.Start == audit.ChainStartNone:
		p("NOT COVERED: %s (ids %d-%d) have no hash, so none of them can be verified.",
			events(r.LegacyRows), r.LegacyFirstID, r.LegacyLastID)
		p("Either they were written before the chain existed, or their hashes were removed. " +
			"Events written by this version of denkeeper serve are chained.")
	default:
		p("OK: %s verified (ids %d-%d).", events(r.Verified), r.FirstID, r.HeadID)
		writeNotCovered(p, r)
		writeChainStart(p, r)
		p("Head: id %d hash %s", r.HeadID, r.HeadHash)
	}
}

type printer func(format string, args ...any)

func writeNotCovered(p printer, r *audit.ChainReport) {
	if r.LegacyRows > 0 {
		p("Not covered: %d older %s (ids %d-%d) were written before the chain existed. They age out with [audit] retention_days.",
			r.LegacyRows, plural(r.LegacyRows, "event", "events"), r.LegacyFirstID, r.LegacyLastID)
	}
}

func writeChainStart(p printer, r *audit.ChainReport) {
	if r.Start == audit.ChainStartCheckpoint {
		p("Chain start: prune checkpoint %d accounts for the events up to id %d that retention removed.",
			r.CheckpointID, r.PrunedThroughID)
		return
	}
	p("Chain start: event %d is the first chained event.", r.FirstID)
}

func writeBreak(p printer, r *audit.ChainReport) {
	b := r.Break
	p("BROKEN at event %d (%s %s/%s): %s", b.ID, b.Timestamp, b.Category, b.Action, breakReason(b))
	switch b.Kind {
	case audit.BreakHashMismatch:
		p("  expected hash: %s", orEmpty(b.Expected))
		p("  stored hash:   %s", orEmpty(b.Stored))
	case audit.BreakLinkMismatch:
		p("  expected prev_hash: %s", orEmpty(b.Expected))
		p("  stored prev_hash:   %s", orEmpty(b.Stored))
	case audit.BreakMissingStart:
		p("  expected prev_hash: (empty) or a prune checkpoint's pruned_through_hash")
		p("  stored prev_hash:   %s", orEmpty(b.Stored))
	}
	if r.Verified > 0 {
		p("Verified before the break: %s (ids %d-%d).", events(r.Verified), r.FirstID, r.HeadID)
	} else {
		p("No events verified before the break.")
	}
	writeNotCovered(p, r)
	if r.Unchecked > 0 {
		p("Not checked: %s after id %d.", events(r.Unchecked), b.ID)
	}
}

func breakReason(b *audit.ChainBreak) string {
	switch b.Kind {
	case audit.BreakHashMismatch:
		return "the event does not match its stored hash. It was changed after it was written."
	case audit.BreakLinkMismatch:
		return fmt.Sprintf("its prev_hash is not the hash of event %d before it. Events were removed, added or reordered here.", b.PrevID)
	case audit.BreakUnhashedRow:
		return "the event has no hash but follows chained events. It was written by an older version, written outside denkeeper, or its hash was removed."
	case audit.BreakMissingStart:
		return "it is the first chained event but points at an earlier one, and no prune checkpoint accounts for the gap. Earlier events were removed outside retention."
	}
	return b.Kind
}

func events(n int64) string { return fmt.Sprintf("%d %s", n, plural(n, "event", "events")) }

func plural(n int64, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func orEmpty(s string) string {
	if s == "" {
		return "(empty)"
	}
	return s
}
