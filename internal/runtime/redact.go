package runtime

import "regexp"

var secretRE = regexp.MustCompile(`(?i)(sk-[a-z0-9_-]{16,}|gh[pousr]_[a-z0-9]{20,}|AKIA[0-9A-Z]{16}|xox[baprs]-[a-z0-9-]{10,}|eyJ[a-z0-9_-]{10,}\.[a-z0-9_-]{10,}\.[a-z0-9_-]{10,}|((api[_-]?key|token|secret|passwd|password|authorization)["']?\s*[=:]\s*)["']?[^\s"']{6,})`)

func Redact(s string) string {
	return secretRE.ReplaceAllStringFunc(s, func(m string) string {
		if sub := secretRE.FindStringSubmatch(m); sub[2] != "" {
			return sub[2] + "[REDACTED]"
		}
		return "[REDACTED]"
	})
}
