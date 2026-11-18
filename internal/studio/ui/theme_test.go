package ui

import (
	"strings"
	"testing"
)

// SGR colour codes: 30–37/90–97 (basic), 38;… (extended) foregrounds.
func hasColour(s string) bool {
	for _, code := range []string{"[3", "[9", ";3", ";9", "38;"} {
		if strings.Contains(s, "\x1b"+code) || strings.Contains(s, code+"8;") {
			return true
		}
	}
	return strings.Contains(s, "38;2;") || strings.Contains(s, "38;5;")
}

func TestThemes(t *testing.T) {
	all := []Tone{ToneNormal, ToneMuted, ToneAccent, ToneOK, ToneWarn, ToneError, ToneActive}
	mono := ThemeByName("mono", false)
	for _, tone := range all {
		if out := mono.Render(tone, "x"); hasColour(out) {
			t.Errorf("mono theme emits colour for tone %d: %q", tone, out)
		}
	}
	// NO_COLOR wins over the configured theme.
	if ThemeByName("caffe", true).Name != "mono" {
		t.Error("NO_COLOR did not force mono")
	}
	// The caffè accent is the brand Caramel #C5A27B (197,162,123).
	if out := ThemeByName("caffe", false).Render(ToneAccent, "x"); !strings.Contains(out, "197;162;123") {
		t.Errorf("caffe accent is not Caramel: %q", out)
	}
	// High contrast never dims text.
	if out := ThemeByName("high-contrast", false).Render(ToneMuted, "x"); out != "x" {
		t.Errorf("high-contrast muted text is styled: %q", out)
	}
	if ThemeByName("nope", false).Name != "caffe" {
		t.Error("unknown theme did not fall back to caffe")
	}
	// Styling never changes what is written, only how.
	for _, name := range Themes {
		th := ThemeByName(name, false)
		for _, tone := range all {
			if got := stripSGR(th.Render(tone, "agent ✓ ok")); got != "agent ✓ ok" {
				t.Errorf("%s/%d changed the text: %q", name, tone, got)
			}
		}
	}
}

func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
