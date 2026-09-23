package logging

import "regexp"

// Masked replaces secret values in logged text.
const Masked = "***masked***"

var secretPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	// Authorization: Bearer abc, Authorization=Basic abc
	{regexp.MustCompile(`(?i)(authorization["']?\s*[:=]\s*(?:bearer\s+|basic\s+|token\s+)?)[^\s"',;]+`), "${1}" + Masked},
	// A bearer token anywhere else.
	{regexp.MustCompile(`(?i)(\bbearer\s+)[A-Za-z0-9._~+/=-]+`), "${1}" + Masked},
	// password=x, token: x, api_key=x, "secret":"x"
	{regexp.MustCompile(`(?i)((?:password|passwd|pwd|token|secret|api[_-]?key|access[_-]?key|client[_-]?secret)["']?\s*[:=]\s*["']?)[^\s"'&,;]+`), "${1}" + Masked},
	// --password x, --token x
	{regexp.MustCompile(`(?i)(--?(?:password|passwd|token|secret|api-?key)\s+)[^\s-]\S*`), "${1}" + Masked},
	// user:password@host in URLs
	{regexp.MustCompile(`(://[^/\s:@]+:)[^@\s/]+@`), "${1}" + Masked + "@"},
}

// Mask replaces values that look like passwords, tokens and API keys.
// It covers the usual spellings in command lines and program output; it
// cannot recognise every secret, so secrets belong in env rather than in
// command arguments.
func Mask(s string) string {
	for _, p := range secretPatterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}
