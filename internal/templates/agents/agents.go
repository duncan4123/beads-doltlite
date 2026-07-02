// Package agents provides embedded AGENTS.md templates for bd init and setup.
package agents

import (
	_ "embed"
	"strings"
)

//go:embed defaults/agents.md.tmpl
var defaultTemplate string

//go:embed defaults/beads-section.md
var beadsSection string

// EmbeddedDefault returns the full AGENTS.md template content.
func EmbeddedDefault() string {
	return defaultTemplate
}

// EmbeddedDefaultWithOpts returns the full AGENTS.md template content with
// conditional sync guidance applied.
func EmbeddedDefaultWithOpts(opts RenderOpts) string {
	content := defaultTemplate
	if replaced, _, err := ReplaceSectionWithOpts(content, ProfileFull, opts); err == nil {
		content = replaced
	}
	return ApplyRenderOptsToContent(content, opts)
}

// ApplyRenderOptsToContent removes generated remote-sync guidance outside the
// managed Beads section for local-only stores. The marked section is left
// untouched so its hash metadata continues to describe its exact body.
func ApplyRenderOptsToContent(content string, opts RenderOpts) string {
	if opts.HasRemote && !opts.NoPush {
		return content
	}

	beginIdx := strings.Index(content, "<!-- BEGIN BEADS INTEGRATION")
	if beginIdx == -1 {
		return stripDoltPushReferences(content)
	}
	endMarker := "<!-- END BEADS INTEGRATION -->"
	endRel := strings.Index(content[beginIdx:], endMarker)
	if endRel == -1 {
		return stripDoltPushReferences(content)
	}
	endOfEnd := beginIdx + endRel + len(endMarker)

	prefix := stripDoltPushReferences(content[:beginIdx])
	section := content[beginIdx:endOfEnd]
	suffix := stripDoltPushReferences(content[endOfEnd:])
	return prefix + section + suffix
}

// EmbeddedBeadsSection returns the beads integration section with markers.
// The returned string is trimmed to match the existing agentsBeadsSection behavior
// (no trailing newline after the end marker).
func EmbeddedBeadsSection() string {
	return strings.TrimRight(beadsSection, "\n") + "\n"
}
