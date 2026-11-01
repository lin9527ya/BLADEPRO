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

// srcRedactionMarker tells the report validator that a packet body was shortened
// by credential redaction, so Content-Length comparison must be skipped.
const srcRedactionMarker = "[敏感字段已脱敏]"

// redactSrcRawRequest renders one proof/verification request for the external
// report. It keeps the request line and header names so the packet is still
// reproducible, masks credential-bearing headers/query values and common
// JSON/form secret fields, and marks the body when redaction changed it. The raw
// bytes stay in the internal evidence store.
func redactSrcRawRequest(reqHead, body string) (string, bool) {
	head := strings.TrimRight(strings.ReplaceAll(reqHead, "\r\n", "\n"), "\n")
	lines := strings.Split(head, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	changed := false
	if rl := redactRequestLine(lines[0]); rl != lines[0] {
		lines[0] = rl
		changed = true
	}
	headers := lines[1:]
	if redacted := redactHeaderLines(headers); len(redacted) > 0 {
		for i := range redacted {
			if redacted[i] != headers[i] {
				changed = true
				headers[i] = redacted[i]
			}
		}
	}
	out := strings.Join(lines, "\n")
	if body == "" {
		return out, changed
	}
	redactedBody := redactPocBody(headers, []byte(body))
	out += "\n\n" + string(redactedBody)
	if string(redactedBody) != body {
		out += "\n" + srcRedactionMarker
		changed = true
	}
	return out, changed
}

func redactRequestLine(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return line
	}
	fields[1] = redactURL(fields[1])
	return strings.Join(fields, " ")
}
