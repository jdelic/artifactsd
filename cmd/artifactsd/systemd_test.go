package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testOpts(component systemdComponent, user bool, runtime string) *systemdOpts {
	return &systemdOpts{
		component:   component,
		user:        user,
		runtime:     runtime,
		image:       defaultImage,
		execPath:    "/usr/local/bin/" + runtime, // stand-in; "local" is not a real binary
		regenerated: "artifactsd systemd (test)",
	}
}

func mustContain(t *testing.T, unit string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(unit, w) {
			t.Errorf("unit missing %q:\n%s", w, unit)
		}
	}
}

func mustNotContain(t *testing.T, unit string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if strings.Contains(unit, w) {
			t.Errorf("unit must not contain %q:\n%s", w, unit)
		}
	}
}

func TestRenderSystemdUnit_ServerLocalSystem(t *testing.T) {
	o := testOpts(serverComponent, false, "local")
	o.execPath = "/usr/local/bin/artifactsd"
	unit := renderSystemdUnit(o)
	mustContain(
		t, unit,
		"Description=artifactsd server",
		"User=artifactsd",
		"EnvironmentFile=/etc/artifactsd.env",
		"ExecStart=/usr/local/bin/artifactsd serve --data %S/artifactsd",
		"StateDirectory=artifactsd",
		"ProtectSystem=strict",
		"WantedBy=multi-user.target",
	)
	mustNotContain(t, unit, "docker", "DynamicUser")
}

func TestRenderSystemdUnit_ServerLocalUser(t *testing.T) {
	unit := renderSystemdUnit(testOpts(serverComponent, true, "local"))
	mustContain(
		t, unit,
		"EnvironmentFile=%E/artifactsd/artifactsd.env",
		"WantedBy=default.target",
	)
	// ProtectHome/User= would break or is meaningless under the user manager.
	mustNotContain(t, unit, "ProtectHome", "User=artifactsd", "AmbientCapabilities", "WantedBy=multi-user.target")
}

func TestRenderSystemdUnit_MCPLocalSystem(t *testing.T) {
	o := testOpts(mcpComponent, false, "local")
	o.execPath = "/usr/local/bin/artifactsd"
	unit := renderSystemdUnit(o)
	mustContain(
		t, unit,
		"Description=artifactsd MCP adapter",
		"DynamicUser=yes",
		"EnvironmentFile=/etc/artifactsd-mcp.env",
		"ExecStart=/usr/local/bin/artifactsd mcp",
	)
	// The MCP adapter is stateless: no data dir, and no --data flag.
	mustNotContain(t, unit, "StateDirectory", "--data")
}

func TestRenderSystemdUnit_ServerDockerSystem(t *testing.T) {
	unit := renderSystemdUnit(testOpts(serverComponent, false, "docker"))
	mustContain(
		t, unit,
		"Requires=docker.service",
		"ExecStartPre=-/usr/local/bin/docker rm -f artifactsd",
		"ExecStart=/usr/local/bin/docker run --rm --name artifactsd --env-file /etc/artifactsd.env -p 8080:8080 -v artifactsd-data:/data "+defaultImage,
		"ExecStop=/usr/local/bin/docker stop artifactsd",
	)
	// The image's default command is already `serve`.
	mustNotContain(t, unit, defaultImage+" serve", "StateDirectory")
}

func TestRenderSystemdUnit_MCPPodmanUser(t *testing.T) {
	unit := renderSystemdUnit(testOpts(mcpComponent, true, "podman"))
	mustContain(
		t, unit,
		"ExecStart=/usr/local/bin/podman run --rm --name artifactsd-mcp --env-file %E/artifactsd/artifactsd-mcp.env -p 8081:8081 "+defaultImage+" mcp",
		"WantedBy=default.target",
	)
	// podman has no daemon, and the user manager can't depend on system units.
	mustNotContain(t, unit, "Requires=docker.service", "-v artifactsd-data")
}

func TestSystemdInstallPaths(t *testing.T) {
	sysUnit, sysEnv, err := systemdInstallPaths(testOpts(serverComponent, false, "local"), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if sysUnit != "/etc/systemd/system/artifactsd.service" || sysEnv != "/etc/artifactsd.env" {
		t.Errorf("system paths = %q, %q", sysUnit, sysEnv)
	}

	env := map[string]string{"HOME": "/home/u"}
	getenv := func(k string) string { return env[k] }
	usrUnit, usrEnv, err := systemdInstallPaths(testOpts(mcpComponent, true, "local"), getenv)
	if err != nil {
		t.Fatal(err)
	}
	if usrUnit != "/home/u/.config/systemd/user/artifactsd-mcp.service" || usrEnv != "/home/u/.config/artifactsd/artifactsd-mcp.env" {
		t.Errorf("user paths = %q, %q", usrUnit, usrEnv)
	}

	env["XDG_CONFIG_HOME"] = "/xdg"
	usrUnit, usrEnv, err = systemdInstallPaths(testOpts(serverComponent, true, "local"), getenv)
	if err != nil {
		t.Fatal(err)
	}
	if usrUnit != "/xdg/systemd/user/artifactsd.service" || usrEnv != "/xdg/artifactsd/artifactsd.env" {
		t.Errorf("XDG paths = %q, %q", usrUnit, usrEnv)
	}

	if _, _, err := systemdInstallPaths(testOpts(serverComponent, true, "local"), func(string) string { return "" }); err == nil {
		t.Error("expected error for --user without HOME")
	}
}

func TestSystemdCommand_PrintsUnit(t *testing.T) {
	out, _, err := execute(t, "", "systemd")
	if err != nil {
		t.Fatalf("systemd: %v", err)
	}
	mustContain(t, out, "[Unit]", "[Service]", "[Install]", "ExecStart=", "serve --data %S/artifactsd")
}

func TestSystemdCommand_FlagValidation(t *testing.T) {
	if _, _, err := execute(t, "", "systemd", "--runtime", "lxc"); err == nil || !strings.Contains(err.Error(), "unknown --runtime") {
		t.Errorf("bad runtime error = %v", err)
	}
	if _, _, err := execute(t, "", "systemd", "--server", "--mcp"); err == nil {
		t.Error("expected --server/--mcp to be mutually exclusive")
	}
	if _, _, err := execute(t, "", "systemd", "--image", "x:y"); err == nil || !strings.Contains(err.Error(), "--image") {
		t.Errorf("--image with local runtime error = %v", err)
	}
}

func TestSystemdCommand_InstallUser(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)

	out, _, err := execute(t, "", "systemd", "--mcp", "--user", "--runtime", "podman", "--install")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	mustContain(t, out, "installed systemd unit:", "created env-file skeleton:", "systemctl --user")

	unit, err := os.ReadFile(filepath.Join(cfg, "systemd", "user", "artifactsd-mcp.service"))
	if err != nil {
		t.Fatalf("read installed unit: %v", err)
	}
	mustContain(t, string(unit), "podman run", "WantedBy=default.target")

	envPath := filepath.Join(cfg, "artifactsd", "artifactsd-mcp.env")
	skel, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read env skeleton: %v", err)
	}
	mustContain(t, string(skel), "ARTIFACTSD_BASE_URL", "ARTIFACTSD_MCP_TOKEN")

	// A reinstall must not clobber an edited env file.
	if err := os.WriteFile(envPath, []byte("ARTIFACTSD_MCP_TOKEN=real-secret\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	out, _, err = execute(t, "", "systemd", "--mcp", "--user", "--runtime", "podman", "--install")
	if err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	mustContain(t, out, "using existing env file:")
	got, _ := os.ReadFile(envPath)
	if string(got) != "ARTIFACTSD_MCP_TOKEN=real-secret\n" {
		t.Errorf("reinstall clobbered env file: %q", got)
	}
}
