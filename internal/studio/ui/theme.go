package ui

import (
	"charm.land/lipgloss/v2"
)

type Tone int

const (
	ToneNormal Tone = iota
	ToneMuted
	ToneAccent
	ToneOK
	ToneWarn
	ToneError
	ToneActive
)

type Theme struct {
	Name  string
	tones map[Tone]lipgloss.Style
}

const (
	Espresso  = "#241A16"
	Secondary = "#30231D"
	Cream     = "#F3E7D5"
	Caramel   = "#C5A27B"
	Muted     = "#A99583"
)

var Themes = []string{"caffe", "high-contrast", "mono"}

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
			ToneMuted:  lipgloss.NewStyle(),
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

func (t Theme) Render(tone Tone, s string) string {
	st, ok := t.tones[tone]
	if !ok || s == "" {
		return s
	}
	return st.Render(s)
}
