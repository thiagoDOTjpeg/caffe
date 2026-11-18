package studio

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// special keys, encoded the way Bubble Tea decodes them from the terminal.
var special = map[string]tea.Key{
	"space":     {Code: tea.KeySpace, Text: " "},
	"esc":       {Code: tea.KeyEscape},
	"enter":     {Code: tea.KeyEnter},
	"backspace": {Code: tea.KeyBackspace},
	"up":        {Code: tea.KeyUp},
	"down":      {Code: tea.KeyDown},
	"tab":       {Code: tea.KeyTab},
	"shift+tab": {Code: tea.KeyTab, Mod: tea.ModShift},
}

// keyPress builds the message for a key name like "q", "ctrl+c" or "space",
// as a real terminal would produce it, so tests exercise the real key names.
func keyPress(s string) tea.KeyPressMsg {
	if k, ok := special[s]; ok {
		return tea.KeyPressMsg(k)
	}
	if mod, key, ok := strings.Cut(s, "+"); ok && mod == "ctrl" {
		return tea.KeyPressMsg(tea.Key{Code: []rune(key)[0], Mod: tea.ModCtrl})
	}
	return tea.KeyPressMsg(tea.Key{Code: []rune(s)[0], Text: s})
}
