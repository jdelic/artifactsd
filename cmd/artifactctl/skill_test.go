package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdelic/artifactsd"
)

func TestSkillBody_StripsFrontmatter(t *testing.T) {
	body := skillBody()
	if strings.HasPrefix(body, "---") {
		t.Fatalf("skillBody still has frontmatter: %q", body[:40])
	}
	if !strings.Contains(body, "Publishing artifacts with artifactsd") {
		t.Errorf("skillBody missing expected heading: %q", body[:min(80, len(body))])
	}
}

func TestAgentTarget_Resolve(t *testing.T) {
	env := envFrom(map[string]string{"HOME": "/home/u"})

	for name, target := range skillTargets {
		local, fellBack, err := target.resolve(false, env)
		if err != nil {
			t.Fatalf("%s: local resolve: %v", name, err)
		}
		if target.local != nil {
			if fellBack {
				t.Errorf("%s: local resolve reported fallback", name)
			}
			if local != *target.local {
				t.Errorf("%s: local = %+v, want %+v", name, local, *target.local)
			}
		} else {
			if !fellBack {
				t.Errorf("%s: expected fallback to user-wide location", name)
			}
			if !strings.HasPrefix(local.path, "/home/u") {
				t.Errorf("%s: fallback path = %q, want prefix /home/u", name, local.path)
			}
		}

		global, fellBack, err := target.resolve(true, env)
		if err != nil {
			t.Fatalf("%s: global resolve: %v", name, err)
		}
		if fellBack {
			t.Errorf("%s: global resolve reported fallback", name)
		}
		if !strings.HasPrefix(global.path, "/home/u") {
			t.Errorf("%s: global = %q, want prefix /home/u", name, global.path)
		}
	}
}

func TestAgentTarget_Resolve_SharedAgentsDir(t *testing.T) {
	env := envFrom(map[string]string{"HOME": "/home/u"})
	want := filepath.Join(".agents", "skills", "artifactsd", "SKILL.md")
	wantGlobal := "/home/u/.agents/skills/artifactsd/SKILL.md"
	for _, name := range []string{"codex", "opencode", "copilot"} {
		local, _, err := skillTargets[name].resolve(false, env)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if local.path != want || local.mode != modeCopySkill {
			t.Errorf("%s: local = %+v, want copy to %q", name, local, want)
		}
		global, _, err := skillTargets[name].resolve(true, env)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if global.path != wantGlobal || global.mode != modeCopySkill {
			t.Errorf("%s: global = %+v, want copy to %q", name, global, wantGlobal)
		}
	}
}

func TestAgentTarget_Resolve_CrushHonorsXDGConfigHome(t *testing.T) {
	target := skillTargets["crush"]

	global, _, err := target.resolve(true, envFrom(map[string]string{"HOME": "/home/u", "XDG_CONFIG_HOME": "/xdg"}))
	if err != nil {
		t.Fatal(err)
	}
	want := "/xdg/crush/skills/artifactsd/SKILL.md"
	if global.path != want {
		t.Errorf("global with XDG_CONFIG_HOME = %q, want %q", global.path, want)
	}

	global, _, err = target.resolve(true, envFrom(map[string]string{"HOME": "/home/u"}))
	if err != nil {
		t.Fatal(err)
	}
	want = "/home/u/.config/crush/skills/artifactsd/SKILL.md"
	if global.path != want {
		t.Errorf("global without XDG_CONFIG_HOME = %q, want %q", global.path, want)
	}
}

func TestAgentTarget_Resolve_NoHome(t *testing.T) {
	if _, _, err := skillTargets["claude"].resolve(true, envFrom(nil)); err == nil {
		t.Fatal("expected error when $HOME is unset")
	}
	// hermes falls back to the user-wide location even without --global, so
	// it too needs $HOME.
	if _, _, err := skillTargets["hermes"].resolve(false, envFrom(nil)); err == nil {
		t.Fatal("expected hermes local install to error when $HOME is unset")
	}
}

func TestInstallSkill_CopyMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skills", "artifactsd", "SKILL.md")
	if err := installSkill(path, modeCopySkill); err != nil {
		t.Fatalf("installSkill: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read installed file: %v", err)
	}
	if string(got) != artifactsd.SkillMD {
		t.Errorf("installed content does not match SKILL.md verbatim")
	}
}

func TestMergeSkillBlock_CreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := mergeSkillBlock(path, "hello from artifactsd"); err != nil {
		t.Fatalf("mergeSkillBlock: %v", err)
	}
	got := readFile(t, path)
	if !strings.Contains(got, mergeBeginMarker) || !strings.Contains(got, mergeEndMarker) {
		t.Fatalf("missing markers in:\n%s", got)
	}
	if !strings.Contains(got, "hello from artifactsd") {
		t.Fatalf("missing body in:\n%s", got)
	}
}

func TestMergeSkillBlock_PreservesExistingContentAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(path, []byte("# Project notes\n\nDo not touch this.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := mergeSkillBlock(path, "version one"); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	first := readFile(t, path)
	if !strings.Contains(first, "Do not touch this.") {
		t.Fatalf("first merge lost existing content:\n%s", first)
	}
	if !strings.Contains(first, "version one") {
		t.Fatalf("first merge missing body:\n%s", first)
	}

	if err := mergeSkillBlock(path, "version two"); err != nil {
		t.Fatalf("second merge: %v", err)
	}
	second := readFile(t, path)
	if !strings.Contains(second, "Do not touch this.") {
		t.Fatalf("second merge lost existing content:\n%s", second)
	}
	if strings.Contains(second, "version one") {
		t.Fatalf("second merge left stale body:\n%s", second)
	}
	if !strings.Contains(second, "version two") {
		t.Fatalf("second merge missing updated body:\n%s", second)
	}
	if strings.Count(second, mergeBeginMarker) != 1 {
		t.Fatalf("expected exactly one marker block, got:\n%s", second)
	}
}

// runCLIHome is like runCLI but with a stable, inspectable $HOME.
func runCLIHome(t *testing.T, home string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	env := envFrom(map[string]string{"HOME": home})
	code := run(context.Background(), args, &out, &errOut, env)
	return code, out.String(), errOut.String()
}

func TestRun_SkillInstall_UnknownAgent(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	code, _, errOut := runCLI(t, "skill", "install", "nonexistent-agent")
	if code != exitUsage {
		t.Fatalf("exit = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(errOut, "unknown agent") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestRun_SkillInstall_Claude(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	code, out, errOut := runCLI(t, "skill", "install", "claude")
	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, errOut)
	}
	if !strings.Contains(out, "installed artifactsd skill for claude") {
		t.Errorf("stdout = %q", out)
	}
	got := readFile(t, filepath.Join(dir, ".claude", "skills", "artifactsd", "SKILL.md"))
	if !strings.HasPrefix(got, "---\n") {
		t.Errorf("expected SKILL.md frontmatter, got: %q", got[:min(40, len(got))])
	}
}

func TestRun_SkillInstall_CodexCopiesToAgentsSkills(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	code, _, errOut := runCLI(t, "skill", "install", "codex")
	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, errOut)
	}
	got := readFile(t, filepath.Join(dir, ".agents", "skills", "artifactsd", "SKILL.md"))
	if got != artifactsd.SkillMD {
		t.Errorf("expected verbatim SKILL.md copy in .agents/skills")
	}
}

func TestRun_SkillInstall_HermesFallsBackToUserDir(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	chdir(t, dir)
	code, out, errOut := runCLIHome(t, home, "skill", "install", "hermes")
	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, errOut)
	}
	if !strings.Contains(out, "no project-local skills location") {
		t.Errorf("expected fallback notice, stdout = %q", out)
	}
	got := readFile(t, filepath.Join(home, ".hermes", "skills", "artifactsd", "SKILL.md"))
	if got != artifactsd.SkillMD {
		t.Errorf("expected verbatim SKILL.md copy in ~/.hermes/skills")
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("expected nothing written to the project directory, got %v (err %v)", entries, err)
	}
}

func TestRun_SkillInstall_CrushMergesIntoAgentsMD(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Existing project notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runCLI(t, "skill", "install", "crush")
	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, errOut)
	}
	got := readFile(t, filepath.Join(dir, "AGENTS.md"))
	if !strings.Contains(got, "Existing project notes") {
		t.Errorf("lost existing AGENTS.md content:\n%s", got)
	}
	if strings.Contains(got, "name: artifactsd\ndescription:") {
		t.Errorf("expected frontmatter to be stripped from merged body:\n%s", got)
	}
}

func TestRun_SkillInstall_CopilotGlobal(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	chdir(t, dir)
	code, _, errOut := runCLIHome(t, home, "skill", "install", "copilot", "--global")
	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, errOut)
	}
	got := readFile(t, filepath.Join(home, ".agents", "skills", "artifactsd", "SKILL.md"))
	if got != artifactsd.SkillMD {
		t.Errorf("expected verbatim SKILL.md copy in ~/.agents/skills")
	}
}

// chdir switches to dir for the duration of the test and restores the
// previous working directory afterward.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(prev); err != nil {
			t.Fatal(err)
		}
	})
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
