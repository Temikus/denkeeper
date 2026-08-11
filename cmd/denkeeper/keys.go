package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"

	"github.com/Temikus/denkeeper/internal/api"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/scope"
)

func newKeysCmd() *cobra.Command {
	keysCmd := &cobra.Command{
		Use:   "keys",
		Short: "Manage API keys",
		Long:  "Create and list API keys for authenticating with the web dashboard and REST API.",
	}
	keysCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file path (default: ~/.denkeeper/denkeeper.toml)")

	var createScopes []string
	createCmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new API key",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runKeysCreate(args[0], createScopes)
		},
	}
	createCmd.Flags().StringSliceVarP(&createScopes, "scopes", "s", []string{"admin"},
		"Comma-separated scopes ("+strings.Join(scope.Names(), ", ")+")")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all API keys",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runKeysList()
		},
	}

	keysCmd.AddCommand(createCmd, listCmd)
	return keysCmd
}

func runKeysCreate(name string, scopes []string) error {
	if err := api.ValidateKeyInput(name, scopes); err != nil {
		return err
	}

	dbPath := resolveDBPath(cfgFile)

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return fmt.Errorf("creating data directory: %w", err)
	}

	ks, err := api.NewKeyStore(dbPath)
	if err != nil {
		return fmt.Errorf("opening key store at %s: %w", dbPath, err)
	}

	rec, plaintext, err := ks.Create(context.Background(), name, scopes)
	if err != nil {
		return fmt.Errorf("creating key: %w", err)
	}

	fmt.Printf("Created API key %q (id: %s)\n\n", rec.Name, rec.ID)
	fmt.Printf("  %s\n\n", plaintext)
	fmt.Println("Copy this key now — it will not be shown again.")
	fmt.Printf("Scopes: %s\n", strings.Join(rec.Scopes, ", "))
	return nil
}

func runKeysList() error {
	dbPath := resolveDBPath(cfgFile)

	ks, err := api.NewKeyStore(dbPath)
	if err != nil {
		return fmt.Errorf("opening key store at %s: %w", dbPath, err)
	}

	keys, err := ks.List(context.Background())
	if err != nil {
		return fmt.Errorf("listing keys: %w", err)
	}

	if len(keys) == 0 {
		fmt.Println("No API keys found.")
		fmt.Println("Create one with: denkeeper keys create <name>")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tNAME\tSCOPES\tSTATUS\tCREATED\tLAST USED")
	for _, k := range keys {
		status := "active"
		if k.Revoked {
			status = "revoked"
		}
		lastUsed := "never"
		if k.LastUsedAt != nil {
			lastUsed = k.LastUsedAt.Format("2006-01-02")
		}
		_, _ = fmt.Fprintf(w, "%s...\t%s\t%s\t%s\t%s\t%s\n",
			k.ID[:8],
			k.Name,
			strings.Join(k.Scopes, ", "),
			status,
			k.CreatedAt.Format("2006-01-02"),
			lastUsed,
		)
	}
	return w.Flush()
}

// resolveDBPath returns the SQLite path the server would use for this config,
// applying the same precedence as config.Load: DENKEEPER_MEMORY_DB_PATH >
// [memory].db_path > <data dir>/data/memory.db, where the data dir is
// DENKEEPER_DATA_DIR > data_dir > ~/.denkeeper.
// It intentionally skips full config validation so it works before a valid
// config exists (e.g. on first run when no adapter tokens are configured yet).
func resolveDBPath(cfgPath string) string {
	if cfgPath == "" {
		cfgPath = resolveConfigPath()
	}

	// Partially parse just the path fields — skip adapter/LLM validation.
	type partialMemory struct {
		DBPath string `toml:"db_path"`
	}
	type partialConfig struct {
		DataDir string        `toml:"data_dir"`
		Memory  partialMemory `toml:"memory"`
	}

	var cfg partialConfig
	if data, err := os.ReadFile(cfgPath); err == nil { // #nosec G304 -- path from CLI flag / config, not user input
		if toml.Unmarshal(data, &cfg) != nil {
			cfg = partialConfig{} // malformed config: fall back to defaults
		}
	}

	if v := os.Getenv("DENKEEPER_MEMORY_DB_PATH"); v != "" {
		return v
	}
	if cfg.Memory.DBPath != "" {
		return cfg.Memory.DBPath
	}
	// config.DefaultDBPath already honours DENKEEPER_DATA_DIR, which outranks
	// the TOML data_dir — so only consult data_dir when the env var is unset.
	if cfg.DataDir != "" && os.Getenv("DENKEEPER_DATA_DIR") == "" {
		return filepath.Join(cfg.DataDir, "data", "memory.db")
	}

	return config.DefaultDBPath()
}
