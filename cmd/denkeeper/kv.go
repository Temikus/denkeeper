package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/kv"
)

func newKVCmd() *cobra.Command {
	kvCmd := &cobra.Command{
		Use:   "kv",
		Short: "Maintain the agents' key-value store",
	}
	kvCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file path (default: ~/.denkeeper/denkeeper.toml)")

	var dryRun, yes bool
	backfillCmd := &cobra.Command{
		Use:   "backfill-ttl",
		Short: "Give existing keys the expiry [kv] default_ttl gives new writes",
		Long: "Sets expires_at = updated_at + ttl on every key that has no expiry and matches a [kv] default_ttl prefix, " +
			"whoever wrote it: kv_set, kv_setnx locks, the dashboard, the REST API or the external MCP server. " +
			"Keys stored without an expiry on purpose, such as a kv_set with ttl \"0s\", get one too. " +
			"Keys that already have an expiry, and keys under a prefix with no default, are left alone. " +
			"Run with --dry-run first to see the count per agent and namespace. " +
			"Safe to run while denkeeper serve is up; a second run changes nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runKVBackfillTTL(cmd.Context(), os.Stdout, os.Stdin, resolveConfigPath(), dryRun, yes)
		},
	}
	backfillCmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change and write nothing")
	backfillCmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation prompt")

	kvCmd.AddCommand(backfillCmd)
	return kvCmd
}

func runKVBackfillTTL(ctx context.Context, w io.Writer, r io.Reader, cfgPath string, dryRun, yes bool) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	defaults := cfg.KV.DefaultTTLs()
	if len(defaults) == 0 {
		_, _ = fmt.Fprintf(w, "No [kv] default_ttl is configured in %s; nothing to backfill.\n", cfgPath)
		return nil
	}

	// Stat first: NewSQLiteStore would create an empty database.
	dbPath := cfg.Memory.DBPath
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no database at %s (check [memory] db_path): %w", dbPath, err)
		}
		return fmt.Errorf("checking database %s: %w", dbPath, err)
	}
	store, err := kv.NewSQLiteStore(dbPath)
	if err != nil {
		return fmt.Errorf("opening kv store at %s: %w", dbPath, err)
	}
	defer func() { _ = store.Close() }()

	return backfillTTL(ctx, w, r, store, &cfg.KV, dryRun, yes)
}

func backfillTTL(ctx context.Context, w io.Writer, r io.Reader, store *kv.SQLiteStore, kvCfg *config.KVConfig, dryRun, yes bool) error {
	defaults := kvCfg.DefaultTTLs()
	planned, err := store.BackfillTTL(ctx, defaults, true)
	if err != nil {
		return fmt.Errorf("counting keys: %w", err)
	}
	if len(planned) == 0 {
		_, _ = fmt.Fprintln(w, "No keys need a TTL.")
		return nil
	}
	if err := printBackfill(w, planned, kvCfg.DefaultTTL); err != nil {
		return err
	}
	if dryRun {
		_, _ = fmt.Fprintln(w, "Dry run: nothing was written.")
		return nil
	}
	if !yes {
		ok, answered := confirm(w, r, backfillPrompt(planned))
		if !answered {
			_, _ = fmt.Fprintln(w, "No answer; nothing was written. Pass --yes to run without a prompt.")
			return nil
		}
		if !ok {
			_, _ = fmt.Fprintln(w, "Cancelled.")
			return nil
		}
	}

	done, err := store.BackfillTTL(ctx, defaults, false)
	if err != nil {
		return fmt.Errorf("setting expiries: %w", err)
	}
	keys, expired := backfillTotals(done)
	if expired > 0 {
		_, _ = fmt.Fprintf(w, "Updated %d key(s); %d already expired and will be deleted by the next cleanup.\n", keys, expired)
	} else {
		_, _ = fmt.Fprintf(w, "Updated %d key(s).\n", keys)
	}
	return nil
}

// printBackfill shows each TTL as written in the config, not as a Go duration.
func printBackfill(w io.Writer, counts []kv.BackfillCount, rawTTL map[string]string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "AGENT\tNAMESPACE\tTTL\tKEYS\tALREADY EXPIRED")
	for _, c := range counts {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\n", c.Agent, c.Namespace, rawTTL[c.Namespace], c.Keys, c.AlreadyExpired)
	}
	return tw.Flush()
}

// backfillPrompt names the count and warns about loss only when some keys are
// already past their new expiry. A warning shown on every run gets skipped on
// the run where it matters. The warning goes on its own line first, so the
// question stays short and ends at the prompt.
func backfillPrompt(planned []kv.BackfillCount) string {
	keys, expired := backfillTotals(planned)
	var warning string
	switch {
	case expired == 1:
		warning = "1 key is already past its new expiry and will disappear at once. This cannot be undone.\n"
	case expired > 1:
		warning = fmt.Sprintf("%d keys are already past their new expiry and will disappear at once. This cannot be undone.\n", expired)
	}
	return warning + fmt.Sprintf("Set expiries on %d key(s), including any stored without one on purpose? [y/N]: ", keys)
}

func backfillTotals(counts []kv.BackfillCount) (keys, expired int) {
	for _, c := range counts {
		keys += c.Keys
		expired += c.AlreadyExpired
	}
	return keys, expired
}

// confirm reads one answer. answered is false on a closed stdin (a pipe or
// cron), which never counts as yes.
func confirm(w io.Writer, r io.Reader, prompt string) (ok, answered bool) {
	_, _ = fmt.Fprint(w, prompt)
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		_, _ = fmt.Fprintln(w)
		return false, false
	}
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return answer == "y" || answer == "yes", true
}
