package artifactsd

import (
	"strings"
	"testing"
)

func TestSkillMD_Embedded(t *testing.T) {
	if !strings.HasPrefix(SkillMD, "---\n") {
		t.Fatalf("SkillMD does not start with YAML frontmatter: %q", SkillMD[:min(40, len(SkillMD))])
	}
	if !strings.Contains(SkillMD, "name: artifactsd") {
		t.Errorf("SkillMD missing expected frontmatter name field")
	}
}
