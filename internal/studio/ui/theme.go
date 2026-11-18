package ui

import (
	"charm.land/lipgloss/v2"
)

// Tone is the meaning of a piece of text, not its colour. Themes decide the
// colour; in the mono theme every tone is plain or bold, so meaning must also
// be carried by the symbol and the words (docs/studio/ux-specification.md §4).
type Tone int

const (
	ToneNormal Tone = iota
	ToneMuted       // secondary information
	ToneAccent      // brand accent: product mark, active item, focus
	ToneOK          // completed, healthy
	ToneWarn        // blocked, waiting on a human, stale
	ToneError       // failed, unreachable
	ToneActive      // working right now
)

// Theme renders tones. Body text keeps the terminal's own colours: forcing the
// Cream foreground or the Espresso background would break light terminals.
type Theme struct {
	Name  string
	tones map[Tone]lipgloss.Style
}

// Brand palette (docs/brand.md).
const (
	Espresso  = "#241A16"
	Secondary = "#30231D"
	Cream     = "#F3E7D5"
	Caramel   = "#C5A27B"
	Muted     = "#A99583"
)

// Themes are the accepted names of studio.theme.
var Themes = []string{"caffe", "high-contrast", "mono"}

// ThemeByName returns a theme; noColor (the NO_COLOR convention) forces mono.
// Unknown names fall back to caffe: config validation rejects them earlier.
func ThemeByName(name string, noColor bool) Theme {
	if noColor {
		name = "mono"
	}
	fg := func(c string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(c)) }
	switch name {
	case "mono":
		return Theme{Name: "mono", tones: map[Tone]lipgloss.Style{
			ToneAccent: lipgloss.NewStyle().Bold(true),
			ToneError:  lipgloss.NewStyle().Bold(true),
			ToneWarn:   lipgloss.NewStyle().Bold(true),
		}}
	case "high-contrast":
		return Theme{Name: "high-contrast", tones: map[Tone]lipgloss.Style{
			ToneMuted:  lipgloss.NewStyle(), // no dimmed text at all
			ToneAccent: fg("#FFD27F").Bold(true),
			ToneOK:     fg("#5CFF5C").Bold(true),
			ToneWarn:   fg("#FFFF00").Bold(true),
			ToneError:  fg("#FF5C5C").Bold(true),
			ToneActive: fg("#5CD6FF").Bold(true),
		}}
	default:
		return Theme{Name: "caffe", tones: map[Tone]lipgloss.Style{
			ToneMuted:  fg(Muted),
			ToneAccent: fg(Caramel).Bold(true),
			ToneOK:     fg("#8DB580"),
			ToneWarn:   fg("#E0A84F"),
			ToneError:  fg("#D9695F"),
			ToneActive: fg(Caramel),
		}}
	}
}

// Render styles s with a tone. Width is unchanged: styles only add escapes.
func (t Theme) Render(tone Tone, s string) string {
	st, ok := t.tones[tone]
	if !ok || s == "" {
		return s
	}
	return st.Render(s)
}
