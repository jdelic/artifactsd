// Package artifactsd embeds repository-root assets that need to be available
// at runtime without a source checkout, such as SKILL.md for
// `artifactctl skill install`.
package artifactsd

import _ "embed"

// SkillMD is the contents of the repository's SKILL.md (with YAML
// frontmatter), the agent skill teaching an AI assistant how to publish
// artifacts via the CLI or MCP tools.
//
//go:embed SKILL.md
var SkillMD string
