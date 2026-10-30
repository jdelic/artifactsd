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
// verbatim copy into a skill directory it discovers, or a merged section
// inside a shared project-instructions file it already reads (design/13
// ticket artifactsd-hp9, revised in artifactsd-e8k).
type installMode int

const (
	// modeCopySkill writes SKILL.md verbatim to the target path.
	modeCopySkill installMode = iota
	// modeMergeDoc merges SKILL.md's body (frontmatter stripped) into a
	// marker-delimited block inside the target file, leaving the rest of
	// that file untouched.
	modeMergeDoc
)

func (m installMode) String() string {
	if m == modeMergeDoc {
		return "merge"
	}
	return "copy"
}

// skillDest is one install destination: a file path plus how SKILL.md is
// written there.
type skillDest struct {
	mode installMode
	path string
}

// agentTarget describes where `skill install <agent>` writes, for the
// project-local (default) and user-wide (--global) cases.
type agentTarget struct {
	// local is the project-local destination, relative to the current
	// working directory. A nil local means the agent has no project-local
	// convention at all; the default install then falls back to the
	// user-wide location instead of writing a file the agent never reads.
	local *skillDest
	// global returns the user-wide destination. home is $HOME and configDir
	// is $XDG_CONFIG_HOME (or $HOME/.config when unset).
	global func(home, configDir string) skillDest
}

// skillRel is the directory-per-skill layout shared by every agent that
// implements the Agent Skills standard: <skills dir>/artifactsd/SKILL.md.
var skillRel = filepath.Join("artifactsd", "SKILL.md")

// skillTargets maps agent name to its install target. Paths and conventions
// are per-agent documentation as of 2026-07 (artifactsd-e8k):
//   - claude (Claude Code) reads .claude/skills/ and ~/.claude/skills/.
//   - codex (OpenAI Codex CLI), opencode, and copilot (GitHub Copilot) all
//     discover skills in the cross-agent .agents/skills/ (project) and
//     ~/.agents/skills/ (user) directories, so the three share one copy
//     there rather than each getting its own.
//   - hermes (Hermes Agent) only reads ~/.hermes/skills/ — it has no
//     project-local skills convention, so install always goes user-wide.
//   - crush discovers skills user-wide in $XDG_CONFIG_HOME/crush/skills/
//     but has no project-local skills discovery, so the project-local
//     install merges the skill body into AGENTS.md, which crush reads.
var skillTargets = map[string]agentTarget{
	"claude": {
		local: &skillDest{modeCopySkill, filepath.Join(".claude", "skills", skillRel)},
		global: func(home, _ string) skillDest {
			return skillDest{modeCopySkill, filepath.Join(home, ".claude", "skills", skillRel)}
		},
	},
	"hermes": {
		local: nil,
		global: func(home, _ string) skillDest {
			return skillDest{modeCopySkill, filepath.Join(home, ".hermes", "skills", skillRel)}
		},
	},
	"codex":    agentsDirTarget(),
	"opencode": agentsDirTarget(),
	"copilot":  agentsDirTarget(),
	"crush": {
		local: &skillDest{modeMergeDoc, "AGENTS.md"},
		global: func(_, configDir string) skillDest {
			return skillDest{modeCopySkill, filepath.Join(configDir, "crush", "skills", skillRel)}
		},
	},
}

// agentsDirTarget is the shared target for agents that read the cross-agent
// .agents/skills/ directories (codex, opencode, copilot).
func agentsDirTarget() agentTarget {
	return agentTarget{
		local: &skillDest{modeCopySkill, filepath.Join(".agents", "skills", skillRel)},
		global: func(home, _ string) skillDest {
			return skillDest{modeCopySkill, filepath.Join(home, ".agents", "skills", skillRel)}
		},
	}
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
			"claude gets a verbatim copy of SKILL.md in .claude/skills/. codex, opencode,\n" +
			"and copilot all discover skills in the cross-agent .agents/skills/ directory,\n" +
			"so they share one verbatim copy there. hermes only reads ~/.hermes/skills/, so\n" +
			"its install always goes user-wide. crush discovers skills user-wide (--global);\n" +
			"its project-local install merges the SKILL.md body into a marker-delimited\n" +
			"block in AGENTS.md, and re-running install updates just that block, leaving\n" +
			"the rest of the file alone.",
		Args: exactArgs(1, "<agent>"),
		RunE: func(_ *cobra.Command, args []string) error {
			target, ok := skillTargets[args[0]]
			if !ok {
				return usagef("unknown agent %q: want one of %s", args[0], supportedAgentsList())
			}
			dest, fellBack, err := target.resolve(global, a.env)
			if err != nil {
				return err
			}
			if fellBack && !a.jsonOutput() {
				a.printf("%s has no project-local skills location; installing user-wide\n", args[0])
			}
			if err := installSkill(dest.path, dest.mode); err != nil {
				return err
			}
			return a.printResult(map[string]any{
				"agent": args[0], "mode": dest.mode.String(), "path": dest.path, "user_wide_fallback": fellBack,
			}, "installed artifactsd skill for %s (%s): %s\n", args[0], dest.mode, dest.path)
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

// resolve returns the destination for this target. fellBack reports that a
// project-local install was requested but the agent has no project-local
// location, so the user-wide one is used instead.
func (t agentTarget) resolve(global bool, env func(string) string) (dest skillDest, fellBack bool, err error) {
	if !global && t.local != nil {
		return *t.local, false, nil
	}
	home := env("HOME")
	if home == "" {
		return skillDest{}, false, fmt.Errorf("could not resolve home directory: $HOME is not set")
	}
	configDir := env("XDG_CONFIG_HOME")
	if configDir == "" {
		configDir = filepath.Join(home, ".config")
	}
	return t.global(home, configDir), !global, nil
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
