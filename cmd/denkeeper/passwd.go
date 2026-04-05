package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
)

func newPasswdCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "passwd",
		Short: "Generate a bcrypt hash for dashboard password login",
		Long:  "Reads a password from stdin (with confirmation), outputs a bcrypt hash suitable for api.auth.password_hash in denkeeper.toml.",
		RunE:  runPasswd,
	}
}

func runPasswd(_ *cobra.Command, _ []string) error {
	var password string

	fd := int(os.Stdin.Fd()) // #nosec G115 -- fd is always small enough for int //nolint:gosec
	if term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, "Enter password: ")
		pw1, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return fmt.Errorf("reading password: %w", err)
		}
		fmt.Fprint(os.Stderr, "Confirm password: ")
		pw2, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return fmt.Errorf("reading confirmation: %w", err)
		}
		if string(pw1) != string(pw2) {
			return fmt.Errorf("passwords do not match")
		}
		password = string(pw1)
	} else {
		// Non-interactive: read from piped stdin.
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			password = strings.TrimSpace(scanner.Text())
		}
		if password == "" {
			return fmt.Errorf("empty password")
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), 13)
	if err != nil {
		return fmt.Errorf("generating bcrypt hash: %w", err)
	}

	fmt.Println(string(hash))
	return nil
}
