package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Temikus/denkeeper/internal/agent"
)

func newSessionsCmd() *cobra.Command {
	sessionsCmd := &cobra.Command{
		Use:   "sessions",
		Short: "Manage conversation sessions",
		Long:  "List, inspect, export, and delete conversation sessions stored in the memory database.",
	}
	sessionsCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file path (default: ~/.denkeeper/denkeeper.toml)")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all sessions",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runSessionsList(os.Stdout)
		},
	}

	showCmd := &cobra.Command{
		Use:   "show <session-id>",
		Short: "Show messages in a session",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runSessionsShow(os.Stdout, args[0])
		},
	}

	var deleteYes bool
	deleteCmd := &cobra.Command{
		Use:   "delete <session-id>",
		Short: "Delete a session and its messages",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runSessionsDelete(os.Stdout, os.Stdin, args[0], deleteYes)
		},
	}
	deleteCmd.Flags().BoolVarP(&deleteYes, "yes", "y", false, "skip confirmation prompt")

	var exportFormat string
	exportCmd := &cobra.Command{
		Use:   "export <session-id>",
		Short: "Export a session transcript",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runSessionsExport(os.Stdout, args[0], exportFormat)
		},
	}
	exportCmd.Flags().StringVarP(&exportFormat, "format", "f", "text", "output format: text or json")

	var pruneOlderThan string
	var pruneYes bool
	pruneCmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete sessions older than a given duration",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runSessionsPrune(os.Stdout, os.Stdin, pruneOlderThan, pruneYes)
		},
	}
	pruneCmd.Flags().StringVar(&pruneOlderThan, "older-than", "", "delete sessions older than this duration (e.g. 720h for 30 days)")
	_ = pruneCmd.MarkFlagRequired("older-than")
	pruneCmd.Flags().BoolVarP(&pruneYes, "yes", "y", false, "skip confirmation prompt")

	sessionsCmd.AddCommand(listCmd, showCmd, deleteCmd, exportCmd, pruneCmd)
	return sessionsCmd
}

func openMemoryStore() (*agent.SQLiteMemoryStore, error) {
	dbPath := resolveDBPath(cfgFile)
	store, err := agent.NewSQLiteMemoryStore(dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening memory store at %s: %w", dbPath, err)
	}
	return store, nil
}

func runSessionsList(w *os.File) error {
	store, err := openMemoryStore()
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	convos, _, err := store.ListConversations(ctx, agent.SessionListOpts{})
	if err != nil {
		return fmt.Errorf("listing sessions: %w", err)
	}

	if len(convos) == 0 {
		_, _ = fmt.Fprintln(w, "No sessions found.")
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tADAPTER\tEXT-ID\tMESSAGES\tCOST\tCREATED")
	for _, c := range convos {
		cost, _ := store.ConversationCost(ctx, c.ID)
		id := c.ID
		if len(id) > 24 {
			id = id[:24] + "..."
		}
		extID := c.ExternalID
		if len(extID) > 16 {
			extID = extID[:16] + "..."
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t$%.4f\t%s\n",
			id,
			c.Adapter,
			extID,
			c.MessageCount,
			cost,
			c.CreatedAt.Format("2006-01-02 15:04"),
		)
	}
	return tw.Flush()
}

func runSessionsShow(w *os.File, sessionID string) error {
	store, err := openMemoryStore()
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	messages, err := store.GetMessages(ctx, sessionID, 10000)
	if err != nil {
		return fmt.Errorf("getting messages: %w", err)
	}

	if len(messages) == 0 {
		_, _ = fmt.Fprintln(w, "Session not found or empty.")
		return nil
	}

	_, _ = fmt.Fprintf(w, "Session: %s (%d messages)\n\n", sessionID, len(messages))
	for _, m := range messages {
		content := m.Content
		if len(content) > 120 {
			content = content[:120] + "..."
		}
		content = strings.ReplaceAll(content, "\n", " ")
		_, _ = fmt.Fprintf(w, "[%s] %s: %s\n", m.CreatedAt.Format("2006-01-02 15:04:05"), m.Role, content)
	}
	return nil
}

func runSessionsDelete(w *os.File, r *os.File, sessionID string, skipConfirm bool) error {
	store, err := openMemoryStore()
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	if !skipConfirm {
		_, _ = fmt.Fprintf(w, "Delete session %q and all its messages? [y/N]: ", sessionID)
		scanner := bufio.NewScanner(r)
		if scanner.Scan() {
			answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
			if answer != "y" && answer != "yes" {
				_, _ = fmt.Fprintln(w, "Cancelled.")
				return nil
			}
		}
	}

	if err := store.DeleteConversation(context.Background(), sessionID); err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	_, _ = fmt.Fprintf(w, "Session %q deleted.\n", sessionID)
	return nil
}

// exportMessage is the JSON-serializable message format for export.
type exportMessage struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Tokens    int       `json:"tokens_used"`
	Cost      float64   `json:"cost"`
	CreatedAt time.Time `json:"created_at"`
}

func runSessionsExport(w *os.File, sessionID, format string) error {
	store, err := openMemoryStore()
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	messages, err := store.GetMessages(ctx, sessionID, 10000)
	if err != nil {
		return fmt.Errorf("getting messages: %w", err)
	}

	if len(messages) == 0 {
		_, _ = fmt.Fprintln(w, "Session not found or empty.")
		return nil
	}

	switch format {
	case "json":
		exported := make([]exportMessage, len(messages))
		for i, m := range messages {
			exported[i] = exportMessage{
				Role:      m.Role,
				Content:   m.Content,
				Tokens:    m.TokensUsed,
				Cost:      m.Cost,
				CreatedAt: m.CreatedAt,
			}
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(exported)
	case "text":
		_, _ = fmt.Fprintf(w, "# Session: %s\n\n", sessionID)
		for _, m := range messages {
			_, _ = fmt.Fprintf(w, "## %s [%s]\n\n%s\n\n", m.Role, m.CreatedAt.Format("2006-01-02 15:04:05"), m.Content)
		}
		return nil
	default:
		return fmt.Errorf("unknown format %q (use text or json)", format)
	}
}

func runSessionsPrune(w *os.File, r *os.File, olderThan string, skipConfirm bool) error {
	d, err := time.ParseDuration(olderThan)
	if err != nil {
		return fmt.Errorf("parsing --older-than: %w", err)
	}

	store, err := openMemoryStore()
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	cutoff := time.Now().Add(-d)

	count, err := store.CountConversationsBefore(ctx, cutoff)
	if err != nil {
		return fmt.Errorf("counting sessions: %w", err)
	}

	if count == 0 {
		_, _ = fmt.Fprintln(w, "No sessions older than the specified duration.")
		return nil
	}

	if !skipConfirm {
		_, _ = fmt.Fprintf(w, "Delete %d session(s) created before %s? [y/N]: ", count, cutoff.Format("2006-01-02 15:04"))
		scanner := bufio.NewScanner(r)
		if scanner.Scan() {
			answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
			if answer != "y" && answer != "yes" {
				_, _ = fmt.Fprintln(w, "Cancelled.")
				return nil
			}
		}
	}

	pruned, err := store.PruneConversations(ctx, cutoff)
	if err != nil {
		return fmt.Errorf("pruning sessions: %w", err)
	}
	_, _ = fmt.Fprintf(w, "Pruned %d session(s).\n", pruned)
	return nil
}
