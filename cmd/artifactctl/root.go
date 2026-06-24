package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/jdelic/artifactsd/internal/buildinfo"
	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// outputText and outputJSON are the --output modes (design/10 §1).
const (
	outputText = "text"
	outputJSON = "json"
)

// app holds global flag state and the lazily-resolved API client, shared across
// the command tree.
type app struct {
	// global flags
	baseURL  string
	token    string
	profile  string
	output   string
	quiet    bool
	verbose  bool
	insecure bool

	env func(string) string
	out io.Writer
	err io.Writer

	cli *apiclient.Client
}

// usageError marks an error as a usage problem (exit code 2, design/10 §3).
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

func usagef(format string, args ...any) error {
	return &usageError{err: fmt.Errorf(format, args...)}
}

// newRootCmd builds the artifactctl command tree bound to a.
func newRootCmd(a *app) *cobra.Command {
	root := &cobra.Command{
		Use:           "artifactctl",
		Short:         "artifactctl — command-line client for an artifactsd server",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return a.resolve(cmd)
		},
	}
	root.SetOut(a.out)
	root.SetErr(a.err)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &usageError{err: err}
	})

	pf := root.PersistentFlags()
	pf.StringVar(&a.baseURL, "base-url", "", "artifactsd server base URL")
	pf.StringVar(&a.token, "token", "", "management API token")
	pf.StringVar(&a.profile, "profile", "", "config profile to use")
	pf.StringVar(&a.output, "output", outputText, "output format: text|json")
	pf.BoolVarP(&a.quiet, "quiet", "q", false, "suppress non-essential output")
	pf.BoolVarP(&a.verbose, "verbose", "v", false, "verbose output")
	pf.BoolVar(&a.insecure, "insecure", false, "skip TLS verification (dev only)")

	root.AddCommand(
		a.newCollectionCmd(),
		a.newPutCmd(),
		a.newGetCmd(),
		a.newRmCmd(),
		a.newPutTreeCmd(),
		a.newMetaCmd(),
		a.newShareCmd(),
		a.newTreeCmd(),
		a.newCrawlCmd(),
		a.newTokenCmd(),
		a.newMaintenanceCmd(),
		newVersionCmd(),
	)
	return root
}

// resolve validates global flags and prepares the API client. Commands that do
// not need a server connection (e.g. version) work even without one.
func (a *app) resolve(_ *cobra.Command) error {
	if a.output != outputText && a.output != outputJSON {
		return usagef("invalid --output %q: want text or json", a.output)
	}
	cfg, err := loadFileConfig(defaultConfigPath(a.env))
	if err != nil {
		return err
	}
	r, err := resolveConfig(cfg, a.profile, a.baseURL, a.token, a.env)
	if err != nil {
		return err
	}
	if r.baseURL != "" {
		a.cli = apiclient.New(apiclient.Config{BaseURL: r.baseURL, Token: r.token, Insecure: a.insecure})
	}
	return nil
}

// client returns the resolved API client or a usage error explaining how to
// configure one.
func (a *app) client() (*apiclient.Client, error) {
	if a.cli == nil {
		return nil, usagef("no server configured: set --base-url/--token, %s/%s, or a config profile", envBaseURL, envToken)
	}
	return a.cli, nil
}

// jsonOutput reports whether --output json is selected.
func (a *app) jsonOutput() bool { return a.output == outputJSON }

// printJSON writes v as indented JSON to stdout.
func (a *app) printJSON(v any) error {
	enc := json.NewEncoder(a.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// printf writes a formatted line to stdout unless --quiet is set.
func (a *app) printf(format string, args ...any) {
	if a.quiet {
		return
	}
	_, _ = fmt.Fprintf(a.out, format, args...)
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "artifactctl", buildinfo.String())
			return err
		},
	}
}
