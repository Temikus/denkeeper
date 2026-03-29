package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Temikus/denkeeper/internal/security"
)

func newPluginCmd() *cobra.Command {
	pluginCmd := &cobra.Command{
		Use:   "plugin",
		Short: "Manage plugins",
		Long:  "Generate signing keys, sign plugin binaries, and verify signatures.",
	}

	pluginCmd.AddCommand(newPluginKeygenCmd(), newPluginSignCmd(), newPluginVerifyCmd())
	return pluginCmd
}

func newPluginKeygenCmd() *cobra.Command {
	var outDir string

	cmd := &cobra.Command{
		Use:   "keygen <name>",
		Short: "Generate an Ed25519 signing key pair",
		Long:  "Generates a new Ed25519 key pair and writes <name>.pub and <name>.key PEM files.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runPluginKeygen(args[0], outDir)
		},
	}
	cmd.Flags().StringVarP(&outDir, "output", "o", ".", "directory to write key files to")
	return cmd
}

func newPluginSignCmd() *cobra.Command {
	var keyPath string

	cmd := &cobra.Command{
		Use:   "sign <binary>",
		Short: "Sign a plugin binary",
		Long:  "Creates a detached Ed25519 signature file (<binary>.sig) for the given plugin binary.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runPluginSign(args[0], keyPath)
		},
	}
	cmd.Flags().StringVarP(&keyPath, "key", "k", "", "path to the private key PEM file (required)")
	_ = cmd.MarkFlagRequired("key")
	return cmd
}

func newPluginVerifyCmd() *cobra.Command {
	var keyPaths []string

	cmd := &cobra.Command{
		Use:   "verify <binary>",
		Short: "Verify a plugin binary signature",
		Long:  "Checks the detached signature (<binary>.sig) against one or more trusted public keys.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runPluginVerify(args[0], keyPaths)
		},
	}
	cmd.Flags().StringSliceVarP(&keyPaths, "key", "k", nil, "path(s) to public key PEM file(s) (required, repeatable)")
	_ = cmd.MarkFlagRequired("key")
	return cmd
}

func runPluginKeygen(name, outDir string) error {
	fmt.Println("Generating Ed25519 key pair…")

	pub, priv, err := security.GenerateKeyPair()
	if err != nil {
		return fmt.Errorf("generating key pair: %w", err)
	}

	pubPath := filepath.Join(outDir, name+".pub")
	keyPath := filepath.Join(outDir, name+".key")

	if err := os.WriteFile(pubPath, security.MarshalPublicKeyPEM(pub), 0o644); err != nil {
		return fmt.Errorf("writing public key to %s: %w", pubPath, err)
	}

	if err := os.WriteFile(keyPath, security.MarshalPrivateKeyPEM(priv), 0o600); err != nil {
		return fmt.Errorf("writing private key to %s: %w", keyPath, err)
	}

	fmt.Printf("Public key:  %s\n", pubPath)
	fmt.Printf("Private key: %s\n", keyPath)
	fmt.Println("\nKeep the private key safe — it cannot be recovered.")
	fmt.Println("Add the public key path to [security].trusted_keys in denkeeper.toml.")
	return nil
}

func runPluginSign(binaryPath, keyPath string) error {
	fmt.Printf("Signing %s…\n", binaryPath)

	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("reading private key %s: %w", keyPath, err)
	}

	priv, err := security.ParsePrivateKeyPEM(keyData)
	if err != nil {
		return fmt.Errorf("parsing private key %s: %w", keyPath, err)
	}

	if err := security.SignFile(priv, binaryPath); err != nil {
		return err
	}

	fmt.Printf("Signature written to %s%s\n", binaryPath, security.SignatureFileExtension)
	return nil
}

func runPluginVerify(binaryPath string, keyPaths []string) error {
	fmt.Printf("Verifying %s…\n", binaryPath)

	trustedKeys, err := security.LoadTrustedKeys(keyPaths)
	if err != nil {
		return err
	}

	if err := security.VerifyFile(trustedKeys, binaryPath); err != nil {
		return err
	}

	fmt.Println("Signature OK — signed by a trusted key.")
	return nil
}
