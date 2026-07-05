package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// defaultImage is the multi-arch server image published per release (see
// docs/installation.md § Docker).
const defaultImage = "registry.maurus.net/agent-tools/artifactsd:latest"

// systemdComponent is what the generated unit runs: the HTTP server or the
// MCP adapter (artifactsd-46t).
type systemdComponent struct {
	unitName    string // unit and container name, and env-file basename
	description string
	subcommand  string
	port        string // host/container port for container runtimes
	// stateful components get a StateDirectory and a data volume; the MCP
	// adapter is a stateless proxy and gets neither.
	stateful bool
}

var (
	serverComponent = systemdComponent{
		unitName:    "artifactsd",
		description: "artifactsd server",
		subcommand:  "serve",
		port:        "8080",
		stateful:    true,
	}
	mcpComponent = systemdComponent{
		unitName:    "artifactsd-mcp",
		description: "artifactsd MCP adapter",
		subcommand:  "mcp",
		port:        "8081",
		stateful:    false,
	}
)

// systemdOpts is a fully resolved unit-generation request.
type systemdOpts struct {
	component   systemdComponent
	user        bool
	runtime     string // "local", "docker", or "podman"
	image       string
	execPath    string // artifactsd binary (local) or container runtime binary
	regenerated string // the CLI invocation that produced the unit, for the header
}

// envFileRef is the env-file path as referenced inside the unit. %E expands
// to the manager's configuration directory ($XDG_CONFIG_HOME for user units),
// so the same unit text works for any user.
func (o *systemdOpts) envFileRef() string {
	if o.user {
		return "%E/artifactsd/" + o.component.unitName + ".env"
	}
	return "/etc/" + o.component.unitName + ".env"
}

func newSystemdCmd() *cobra.Command {
	var (
		serverFlag, mcpFlag, user, install bool
		runtime, image                     string
	)
	cmd := &cobra.Command{
		Use:   "systemd [--server|--mcp] [--user] [--runtime local|docker|podman] [--install]",
		Short: "Generate or install a systemd unit that runs artifactsd",
		Long: "Generate a systemd unit that runs the artifactsd server (default) or the\n" +
			"MCP adapter (--mcp) under the selected runtime:\n\n" +
			"  local   run this binary directly (hardened unit, StateDirectory-managed data)\n" +
			"  docker  run the release container image under docker\n" +
			"  podman  run the release container image under podman\n\n" +
			"By default the unit is printed to stdout for inspection or redirection.\n" +
			"--install writes it to the systemd unit directory (/etc/systemd/system, or\n" +
			"~/.config/systemd/user with --user), creates a commented env-file skeleton\n" +
			"next to it if none exists, and prints the systemctl commands to enable it.\n" +
			"Configuration lives in that env file (/etc/artifactsd[-mcp].env system-wide,\n" +
			"$XDG_CONFIG_HOME/artifactsd/ per-user); see docs/configuration.md.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runtime != "local" && runtime != "docker" && runtime != "podman" {
				return fmt.Errorf("unknown --runtime %q: want local, docker, or podman", runtime)
			}
			if cmd.Flags().Changed("image") && runtime == "local" {
				return fmt.Errorf("--image only applies to --runtime docker|podman")
			}
			o := systemdOpts{
				component:   serverComponent,
				user:        user,
				runtime:     runtime,
				image:       image,
				regenerated: regenerateInvocation(mcpFlag, user, runtime, install),
			}
			if mcpFlag {
				o.component = mcpComponent
			}
			if runtime == "local" {
				o.execPath = artifactsdExecPath()
			} else {
				o.execPath = runtimeExecPath(runtime)
			}
			unit := renderSystemdUnit(&o)
			if !install {
				_, _ = fmt.Fprint(cmd.OutOrStdout(), unit)
				unitPath, _, err := systemdInstallPaths(&o, os.Getenv)
				if err == nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "# rerun with --install to write %s\n", unitPath)
				}
				return nil
			}
			return installSystemdUnit(cmd, &o, unit)
		},
	}
	cmd.Flags().BoolVar(&serverFlag, "server", false, "generate the unit for the HTTP server (the default)")
	cmd.Flags().BoolVar(&mcpFlag, "mcp", false, "generate the unit for the MCP adapter instead of the server")
	cmd.Flags().BoolVar(&user, "user", false, "target the per-user systemd manager instead of the system-wide one")
	cmd.Flags().StringVar(&runtime, "runtime", "local", "how the unit runs artifactsd: local, docker, or podman")
	cmd.Flags().StringVar(&image, "image", defaultImage, "container image for --runtime docker|podman")
	cmd.Flags().BoolVar(&install, "install", false, "write the unit and an env-file skeleton instead of printing the unit")
	cmd.MarkFlagsMutuallyExclusive("server", "mcp")
	return cmd
}

// regenerateInvocation reconstructs the command line for the unit header, so
// a reader of the installed unit knows how to reproduce it.
func regenerateInvocation(mcp, user bool, runtime string, install bool) string {
	parts := []string{"artifactsd systemd"}
	if mcp {
		parts = append(parts, "--mcp")
	}
	if user {
		parts = append(parts, "--user")
	}
	parts = append(parts, "--runtime "+runtime)
	if install {
		parts = append(parts, "--install")
	}
	return strings.Join(parts, " ")
}

// artifactsdExecPath is the absolute path of the running binary — the binary
// being asked to generate a unit is the one the unit should run. Falls back
// to the conventional install location if the path cannot be resolved.
func artifactsdExecPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "/usr/local/bin/artifactsd"
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

// runtimeExecPath resolves the container runtime binary. systemd (≥ v239)
// resolves bare names too, but an absolute path keeps the unit unambiguous.
func runtimeExecPath(runtime string) string {
	if p, err := exec.LookPath(runtime); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
	}
	return "/usr/bin/" + runtime
}

func renderSystemdUnit(o *systemdOpts) string {
	var b strings.Builder
	c := o.component
	fmt.Fprintf(&b, "# %s.service — generated by `%s`.\n", c.unitName, o.regenerated)
	fmt.Fprintf(&b, "# Configuration is read from %s.\n\n", o.envFileRef())

	b.WriteString("[Unit]\n")
	fmt.Fprintf(&b, "Description=%s\n", c.description)
	b.WriteString("Documentation=https://github.com/jdelic/artifactsd\n")
	if o.runtime == "docker" && !o.user {
		b.WriteString("After=network-online.target docker.service\n")
		b.WriteString("Requires=docker.service\n")
	} else {
		b.WriteString("After=network-online.target\n")
	}
	b.WriteString("Wants=network-online.target\n\n")

	b.WriteString("[Service]\nType=simple\n")
	if o.runtime == "local" {
		o.renderLocalService(&b)
	} else {
		o.renderContainerService(&b)
	}
	b.WriteString("Restart=on-failure\nRestartSec=5s\n\n")

	b.WriteString("[Install]\n")
	if o.user {
		b.WriteString("WantedBy=default.target\n")
	} else {
		b.WriteString("WantedBy=multi-user.target\n")
	}
	return b.String()
}

// renderLocalService emits the [Service] body for running the binary
// directly, modeled on packaging/systemd/artifactsd.service.
func (o *systemdOpts) renderLocalService(b *strings.Builder) {
	c := o.component
	if !o.user {
		if c.stateful {
			// A dedicated system user must exist; the unit header install
			// steps printed by --install cover creating it.
			b.WriteString("User=artifactsd\nGroup=artifactsd\n")
		} else {
			b.WriteString("DynamicUser=yes\n")
		}
	}
	fmt.Fprintf(b, "EnvironmentFile=%s\n", o.envFileRef())
	if c.stateful {
		// %S is /var/lib for the system manager and $XDG_STATE_HOME for the
		// user manager; StateDirectory= creates it either way.
		fmt.Fprintf(b, "ExecStart=%s %s --data %%S/artifactsd\n", o.execPath, c.subcommand)
		b.WriteString("StateDirectory=artifactsd\nWorkingDirectory=%S/artifactsd\n")
	} else {
		fmt.Fprintf(b, "ExecStart=%s %s\n", o.execPath, c.subcommand)
	}
	b.WriteString("NoNewPrivileges=true\nPrivateTmp=true\n")
	if !o.user {
		// ProtectHome would sever a user unit from its own $HOME (env file,
		// state dir), so the hardening below is system-scope only.
		b.WriteString("AmbientCapabilities=CAP_NET_BIND_SERVICE\n")
		b.WriteString("ProtectSystem=strict\nProtectHome=true\n")
	}
}

// renderContainerService emits the [Service] body for running the release
// image under docker or podman.
func (o *systemdOpts) renderContainerService(b *strings.Builder) {
	c := o.component
	// Remove any leftover container from an unclean stop; `-` ignores the
	// failure when none exists.
	fmt.Fprintf(b, "ExecStartPre=-%s rm -f %s\n", o.execPath, c.unitName)
	run := fmt.Sprintf("%s run --rm --name %s --env-file %s -p %s:%s",
		o.execPath, c.unitName, o.envFileRef(), c.port, c.port)
	if c.stateful {
		run += " -v artifactsd-data:/data"
	}
	run += " " + o.image
	if c.subcommand != "serve" {
		// The image's default command is `serve ...`; other components name
		// their subcommand explicitly.
		run += " " + c.subcommand
	}
	fmt.Fprintf(b, "ExecStart=%s\n", run)
	fmt.Fprintf(b, "ExecStop=%s stop %s\n", o.execPath, c.unitName)
}

// systemdInstallPaths returns where --install writes the unit and the env
// file for this request.
func systemdInstallPaths(o *systemdOpts, getenv func(string) string) (unitPath, envPath string, err error) {
	unitFile := o.component.unitName + ".service"
	envFile := o.component.unitName + ".env"
	if !o.user {
		return "/etc/systemd/system/" + unitFile, "/etc/" + envFile, nil
	}
	configDir := getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home := getenv("HOME")
		if home == "" {
			return "", "", fmt.Errorf("could not resolve home directory: $HOME is not set")
		}
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, "systemd", "user", unitFile),
		filepath.Join(configDir, "artifactsd", envFile), nil
}

// installSystemdUnit writes the unit and, if none exists yet, an env-file
// skeleton, then prints what to do next.
func installSystemdUnit(cmd *cobra.Command, o *systemdOpts, unit string) error {
	unitPath, envPath, err := systemdInstallPaths(o, os.Getenv)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o750); err != nil {
		return fmt.Errorf("create unit directory: %w (system-wide install needs root; use --user or sudo)", err)
	}
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil { //nolint:gosec // unit files are world-readable by convention
		return fmt.Errorf("write %s: %w (system-wide install needs root; use --user or sudo)", unitPath, err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "installed systemd unit: %s\n", unitPath)

	if _, statErr := os.Stat(envPath); os.IsNotExist(statErr) {
		if err := os.MkdirAll(filepath.Dir(envPath), 0o750); err != nil {
			return fmt.Errorf("create env-file directory: %w", err)
		}
		// The env file will hold secrets (bootstrap/MCP tokens), so keep it
		// out of reach of other users.
		if err := os.WriteFile(envPath, []byte(envSkeleton(o)), 0o640); err != nil { //nolint:gosec // 0640 is the intended env-file mode
			return fmt.Errorf("write %s: %w", envPath, err)
		}
		fmt.Fprintf(&b, "created env-file skeleton: %s (edit it before starting)\n", envPath)
	} else {
		fmt.Fprintf(&b, "using existing env file: %s\n", envPath)
	}

	systemctl := "systemctl"
	if o.user {
		systemctl = "systemctl --user"
	}
	b.WriteString("next steps:\n")
	if !o.user && o.runtime == "local" && o.component.stateful {
		b.WriteString("  useradd --system --no-create-home --shell /usr/sbin/nologin artifactsd   # once\n")
	}
	fmt.Fprintf(&b, "  %s daemon-reload\n  %s enable --now %s\n", systemctl, systemctl, o.component.unitName)
	_, _ = fmt.Fprint(cmd.OutOrStdout(), b.String())
	return nil
}

// envSkeleton is the commented starter env file for the component. All keys
// are documented in docs/configuration.md.
func envSkeleton(o *systemdOpts) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Configuration for %s.service (see docs/configuration.md).\n", o.component.unitName)
	if o.component.subcommand == "mcp" {
		b.WriteString("ARTIFACTSD_BASE_URL=https://artifacts.example.com\n")
		b.WriteString("ARTIFACTSD_MCP_TOKEN=change-me\n")
		if o.runtime == "local" {
			b.WriteString("#ARTIFACTSD_MCP_LISTEN=:8081\n")
		} else {
			b.WriteString("# The unit maps host port 8081; keep the in-container listen default (:8081).\n")
		}
		return b.String()
	}
	b.WriteString("ARTIFACTSD_BASE_URL=https://artifacts.example.com\n")
	if o.runtime == "local" {
		b.WriteString("#ARTIFACTSD_LISTEN=:8080\n")
		b.WriteString("# The data directory is set by the unit (--data); no ARTIFACTSD_DATA_DIR needed.\n")
	} else {
		b.WriteString("# The unit maps host port 8080 and mounts the artifactsd-data volume at /data;\n")
		b.WriteString("# keep the in-container defaults (listen :8080, data /data).\n")
	}
	b.WriteString("# Seeds an admin-scoped API token on first boot so you have a way in.\n")
	b.WriteString("# Generate a long random value and drop this line once a real token exists.\n")
	b.WriteString("#ARTIFACTSD_BOOTSTRAP_TOKEN=change-me\n")
	return b.String()
}
