package main

import (
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

func TestAgentTarget_ResolvePath(t *testing.T) {
	env := envFrom(map[string]string{"HOME": "/home/u"})

	for name, target := range skillTargets {
		local, err := target.resolvePath(false, env)
		if err != nil {
			t.Fatalf("%s: local resolvePath: %v", name, err)
		}
		if local != target.localPath {
			t.Errorf("%s: local = %q, want %q", name, local, target.localPath)
		}

		global, err := target.resolvePath(true, env)
		if target.global == nil {
			if err == nil {
				t.Errorf("%s: expected --global to error, got path %q", name, global)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: global resolvePath: %v", name, err)
		}
		if !strings.HasPrefix(global, "/home/u") {
			t.Errorf("%s: global = %q, want prefix /home/u", name, global)
		}
	}
}

func TestAgentTarget_ResolvePath_NoHome(t *testing.T) {
	target := skillTargets["claude"]
	if _, err := target.resolvePath(true, envFrom(nil)); err == nil {
		t.Fatal("expected error when $HOME is unset")
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

func TestRun_SkillInstall_CodexMergesIntoAgentsMD(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Existing project notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runCLI(t, "skill", "install", "codex")
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

func TestRun_SkillInstall_CopilotGlobalUnsupported(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	code, _, errOut := runCLI(t, "skill", "install", "copilot", "--global")
	if code != exitUsage {
		t.Fatalf("exit = %d, want %d, stderr = %q", code, exitUsage, errOut)
	}
	if !strings.Contains(errOut, "not supported") {
		t.Errorf("stderr = %q", errOut)
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
