// Package prompts embeds the role prompt templates.
package prompts

import "embed"

//go:embed *.md
var FS embed.FS
