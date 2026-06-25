package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jdelic/artifactsd"
)

// installMode distinguishes the two ways an agent consumes SKILL.md: a
// verbatim copy into its own skill directory, or a merged section inside a
// shared project-instructions file it already reads (design/13 ticket
// artifactsd-hp9).
type installMode int

const (
	// modeCopySkill writes SKILL.md verbatim to the target path.
	modeCopySkill installMode = iota
	// modeMergeDoc merges SKILL.md's body (frontmatter stripped) into a
	// marker-delimited block inside the target file, leaving the rest of
	// that file untouched.
	modeMergeDoc
)

// agentTarget describes where `skill install <agent>` writes, for the
// project-local (default) and global (--global) cases.
type agentTarget struct {
	mode installMode
	// localPath is relative to the current working directory.
	localPath string
	// global, if set, returns the absolute path under $HOME. A nil global
	// means the agent has no established global/user-wide location.
	global func(home string) string
}

func (m installMode) String() string {
	if m == modeMergeDoc {
		return "merge"
	}
	return "copy"
}

// skillTargets maps agent name to its install target. Paths and conventions
// are per-agent documentation as of 2026-06 (see artifactsd-hp9):
//   - claude (Claude Code) and hermes (Hermes Agent) read a SKILL.md with YAML
//     frontmatter from a per-skill directory.
//   - codex (OpenAI Codex CLI), opencode, and crush read a single shared
//     AGENTS.md (crush also reads CRUSH.md/CLAUDE.md, but is pointed at the
//     same AGENTS.md here so all three merge into one file rather than three).
//   - copilot (GitHub Copilot) reads .github/copilot-instructions.md and has
//     no global/user-wide equivalent.
var skillTargets = map[string]agentTarget{
	"claude": {
		mode:      modeCopySkill,
		localPath: filepath.Join(".claude", "skills", "artifactsd", "SKILL.md"),
		global: func(home string) string {
			return filepath.Join(home, ".claude", "skills", "artifactsd", "SKILL.md")
		},
	},
	"hermes": {
		mode:      modeCopySkill,
		localPath: filepath.Join("skills", "artifactsd", "SKILL.md"),
		global: func(home string) string {
			return filepath.Join(home, ".hermes", "skills", "artifactsd", "SKILL.md")
		},
	},
	"codex": {
		mode:      modeMergeDoc,
		localPath: "AGENTS.md",
		global: func(home string) string {
			return filepath.Join(home, ".codex", "AGENTS.md")
		},
	},
	"opencode": {
		mode:      modeMergeDoc,
		localPath: "AGENTS.md",
		global: func(home string) string {
			return filepath.Join(home, ".config", "opencode", "AGENTS.md")
		},
	},
	"crush": {
		mode:      modeMergeDoc,
		localPath: "AGENTS.md",
		global: func(home string) string {
			return filepath.Join(home, ".config", "crush", "CRUSH.md")
		},
	},
	"copilot": {
		mode:      modeMergeDoc,
		localPath: filepath.Join(".github", "copilot-instructions.md"),
		global:    nil,
	},
}

const (
	mergeBeginMarker = "<!-- BEGIN artifactsd skill (managed by `artifactctl skill install`; edits here will be overwritten) -->"
	mergeEndMarker   = "<!-- END artifactsd skill -->"
)

// newSkillCmd builds `artifactctl skill ...` (artifactsd-hp9).
func (a *app) newSkillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage the artifactsd agent skill",
	}
	cmd.AddCommand(a.newSkillInstallCmd())
	return cmd
}

func (a *app) newSkillInstallCmd() *cobra.Command {
	var global bool
	cmd := &cobra.Command{
		Use:   "install <agent>",
		Short: "Install the artifactsd SKILL.md into an AI agent's tooling",
		Long: "Install the artifactsd SKILL.md into an AI agent's tooling.\n\n" +
			"Supported agents: " + supportedAgentsList() + ".\n\n" +
			"claude and hermes get a verbatim copy of SKILL.md in their own skill\n" +
			"directory. codex, opencode, crush, and copilot get the SKILL.md body merged\n" +
			"into a marker-delimited block in the shared instructions file they read\n" +
			"(AGENTS.md or .github/copilot-instructions.md); re-running install updates\n" +
			"just that block and leaves the rest of the file alone.",
		Args: exactArgs(1, "<agent>"),
		RunE: func(_ *cobra.Command, args []string) error {
			target, ok := skillTargets[args[0]]
			if !ok {
				return usagef("unknown agent %q: want one of %s", args[0], supportedAgentsList())
			}
			path, err := target.resolvePath(global, a.env)
			if err != nil {
				return err
			}
			if err := installSkill(path, target.mode); err != nil {
				return err
			}
			a.printf("installed artifactsd skill for %s (%s): %s\n", args[0], target.mode, path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&global, "global", false, "install into the agent's global/user-wide location instead of the current project")
	return cmd
}

func supportedAgentsList() string {
	names := make([]string, 0, len(skillTargets))
	for name := range skillTargets {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// resolvePath returns the destination file path for this target.
func (t agentTarget) resolvePath(global bool, env func(string) string) (string, error) {
	if !global {
		return t.localPath, nil
	}
	if t.global == nil {
		return "", usagef("--global is not supported for this agent: it has no established user-wide instructions location")
	}
	home := env("HOME")
	if home == "" {
		return "", fmt.Errorf("could not resolve home directory: $HOME is not set")
	}
	return t.global(home), nil
}

// installSkill writes or merges SKILL.md into path according to mode,
// creating any missing parent directories.
func installSkill(path string, mode installMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create directory for %s: %w", path, err)
	}
	switch mode {
	case modeCopySkill:
		if err := os.WriteFile(path, []byte(artifactsd.SkillMD), 0o644); err != nil { //nolint:gosec // not a secret; agent tooling expects a world-readable file
			return fmt.Errorf("write %s: %w", path, err)
		}
		return nil
	case modeMergeDoc:
		return mergeSkillBlock(path, skillBody())
	default:
		return fmt.Errorf("unknown install mode %v", mode)
	}
}

// mergeSkillBlock writes or replaces a marker-delimited block containing body
// inside the file at path, preserving any other content. The file is created
// if it doesn't exist yet.
func mergeSkillBlock(path, body string) error {
	existing, err := os.ReadFile(path) //nolint:gosec // path is a resolved agentTarget path, not arbitrary user input
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	block := mergeBeginMarker + "\n\n" + body + "\n" + mergeEndMarker + "\n"

	content := string(existing)
	start := strings.Index(content, mergeBeginMarker)
	var next string
	switch {
	case start < 0:
		next = content
		if next != "" && !strings.HasSuffix(next, "\n") {
			next += "\n"
		}
		if next != "" {
			next += "\n"
		}
		next += block
	default:
		relEnd := strings.Index(content[start:], mergeEndMarker)
		if relEnd < 0 {
			return fmt.Errorf("%s: found a start marker without a matching end marker; remove the stray %q line and re-run", path, mergeBeginMarker)
		}
		end := start + relEnd + len(mergeEndMarker)
		if end < len(content) && content[end] == '\n' {
			end++
		}
		next = content[:start] + block + content[end:]
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil { //nolint:gosec // not a secret; agent tooling expects a world-readable file
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// skillBody returns SKILL.md with its leading YAML frontmatter stripped, for
// agents whose shared instructions files don't use frontmatter.
func skillBody() string {
	const fence = "---\n"
	s := artifactsd.SkillMD
	if !strings.HasPrefix(s, fence) {
		return strings.TrimSpace(s)
	}
	rest := s[len(fence):]
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(rest[idx+len("\n---\n"):])
}
