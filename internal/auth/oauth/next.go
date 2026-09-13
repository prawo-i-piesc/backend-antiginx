package oauth

import "strings"

const DefaultNext = "/dashboard"

func SanitizeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") {
		return DefaultNext
	}
	if strings.ContainsAny(next, "\\\r\n\t") {
		return DefaultNext
	}
	if len(next) > 1 && (next[1] == '/' || next[1] == '\\') {
		return DefaultNext
	}
	for _, r := range next {
		if r < 0x20 || r == 0x7f {
			return DefaultNext
		}
	}
	return next
}
