package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/buildinfo"
	"github.com/jdelic/artifactsd/internal/config"
	"github.com/jdelic/artifactsd/internal/store"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "artifactsd",
		Short:         "artifactsd — self-contained artifact storage and serving",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newServeCmd(),
		newMigrateCmd(),
		newHashPasswordCmd(),
		newMCPCmd(),
		newVersionCmd(),
	)
	return root
}

// loadConfig resolves config from the subcommand's raw args (flags) and the
// process environment. Subcommands that take server flags use DisableFlagParsing
// so the config package owns flag definitions (design/12).
func loadConfig(args []string, out io.Writer) (*config.Config, error) {
	cfg, err := config.LoadAndValidate(args, os.Getenv, out)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, nil
		}
		return nil, err
	}
	return cfg, nil
}

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "serve [flags]",
		Short:              "Run the HTTP server",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(args, cmd.OutOrStderr())
			if err != nil || cfg == nil {
				return err
			}
			log := audit.NewAppLogger(cmd.ErrOrStderr(), cfg.Server.LogLevel)
			// The HTTP server runtime is implemented in artifactsd-gmu; for now
			// serve doubles as a configuration check.
			log.Warn(
				"server runtime not yet implemented; configuration validated",
				"listen", cfg.Server.Listen,
				"base_url", cfg.Server.BaseURL,
				"data_dir", cfg.Server.DataDir,
				"tls", string(cfg.TLS.Mode),
			)
			return nil
		},
	}
}

func newMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "migrate [flags]",
		Short:              "Apply pending database migrations",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(args, cmd.OutOrStderr())
			if err != nil || cfg == nil {
				return err
			}
			log := audit.NewAppLogger(cmd.ErrOrStderr(), cfg.Server.LogLevel)
			db, err := store.OpenInDir(cmd.Context(), cfg.Server.DataDir)
			if err != nil {
				return err
			}
			defer func() { _ = db.Close() }()
			version, err := db.Migrate(cmd.Context())
			if err != nil {
				return err
			}
			log.Info("migrations applied", "schema_version", version, "data_dir", cfg.Server.DataDir)
			return nil
		},
	}
}

func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "mcp [flags]",
		Short:              "Run the MCP adapter",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(args, cmd.OutOrStderr())
			if err != nil || cfg == nil {
				return err
			}
			log := audit.NewAppLogger(cmd.ErrOrStderr(), cfg.Server.LogLevel)
			// The MCP adapter is implemented in the MCP epic (artifactsd-zqw).
			log.Warn("MCP adapter not yet implemented")
			return nil
		},
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "artifactsd", buildinfo.String())
			return err
		},
	}
}

func newHashPasswordCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "hash-password [password]",
		Short: "Produce an argon2id hash for the admin basic fallback",
		Long: "Produce an argon2id hash for ARTIFACTSD_ADMIN_BASIC_PASSWORD_HASH.\n" +
			"The password may be given as an argument or piped on stdin.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			password, err := readPassword(cmd, args)
			if err != nil {
				return err
			}
			if password == "" {
				return errors.New("empty password")
			}
			hash, err := auth.HashPassword(password)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), hash)
			return err
		},
	}
}

// readPassword returns the password from the positional argument, or the first
// line of stdin when no argument is given.
func readPassword(cmd *cobra.Command, args []string) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	r := bufio.NewReader(cmd.InOrStdin())
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
