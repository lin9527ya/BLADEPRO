package server

import (
	"net/url"
	"regexp"
	"strings"
)

// redactedValue is the placeholder used in externally shared delivery artifacts.
// The original request/response stays in the internal evidence store.
const redactedValue = "[REDACTED]"

var sensitiveBodyKV = regexp.MustCompile(`(?i)("?(?:password|passwd|pwd|token|secret|api[_-]?key|authorization|cookie|session|access_token|refresh_token)"?\s*[:=]\s*)("[^"]*"|'[^']*'|[^&\s,;}]+)`)

// redactHeaderLines masks credential-bearing request headers while preserving the
// header name and ordering needed to reproduce the request. Unknown names pass
// through unchanged so Host/Content-Type/custom protocol headers stay usable.
func redactHeaderLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		name, _, ok := strings.Cut(line, ":")
		if !ok || !looksSecretKey(strings.TrimSpace(name)) {
			out[i] = line
			continue
		}
		out[i] = name + ": " + redactedValue
	}
	return out
}

// redactURL masks sensitive query values (token, key, session, password, ...)
// without changing the path or non-sensitive parameters.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery == "" {
		return raw
	}
	q := u.Query()
	changed := false
	for name := range q {
		if looksSecretKey(name) {
			q.Set(name, redactedValue)
			changed = true
		}
	}
	if !changed {
		return raw
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func contentType(headers []string) string {
	for _, line := range headers {
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "content-type") {
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	return ""
}

// redactPocBody masks common credential fields in JSON/form bodies. It deliberately
// leaves binary and unknown bodies alone; the delivery README still marks the step
// as redacted so an operator knows to use the internal evidence for full replay.
func redactPocBody(headers []string, body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	ct := contentType(headers)
	if !strings.Contains(ct, "json") && !strings.Contains(ct, "x-www-form-urlencoded") {
		return body
	}
	s := string(body)
	redacted := sensitiveBodyKV.ReplaceAllStringFunc(s, func(match string) string {
		sep := strings.IndexAny(match, ":=")
		if sep < 0 {
			return match
		}
		val := match[sep+1:]
		lead := val[:len(val)-len(strings.TrimLeft(val, " \t"))]
		trimmed := strings.TrimSpace(val)
		quote := ""
		if len(trimmed) >= 2 && (trimmed[0] == '"' || trimmed[0] == '\'') {
			quote = string(trimmed[0])
		}
		return match[:sep+1] + lead + quote + redactedValue + quote
	})
	if redacted == s {
		return body
	}
	return []byte(redacted)
}
