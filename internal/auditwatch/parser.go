package auditwatch

import (
	"regexp"
	"strings"
	"time"
)

// Entry is one parsed nsx-audit.log line relevant for API accounting.
type Entry struct {
	TS        time.Time
	Src       string
	User      string
	Operation string
	Status    string
	Raw       string
}

// O formato do nsx-audit.log varia entre versões/subcomponentes do NSX
// (aspas simples vs duplas, Src vs Source address), então cada campo aceita
// as variantes conhecidas em vez de um layout fixo.
var (
	tsRe     = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`)
	opRe     = regexp.MustCompile(`Operation=["']([^"']+)["']`)
	statusRe = regexp.MustCompile(`Operation status=["']([^"']*)["']`)
	userRe   = regexp.MustCompile(`UserName=["']([^"']*)["']`)
	sdUserRe = regexp.MustCompile(`username="([^"]*)"`)
	srcRe    = regexp.MustCompile(`(?i)(?:Src|Source(?:[ _]address)?|client[_ ]?ip)[=:]\s*["']?((?:\d{1,3}\.){3}\d{1,3})`)
)

var tsLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.000",
	"2006-01-02T15:04:05",
}

// ParseLine extracts the accounting fields from one audit log line. Returns
// ok=false for lines without an Operation (not an audited API call) or without
// a parseable timestamp.
func ParseLine(line string) (Entry, bool) {
	op := opRe.FindStringSubmatch(line)
	if op == nil {
		return Entry{}, false
	}
	tsStr := tsRe.FindString(line)
	if tsStr == "" {
		return Entry{}, false
	}
	var ts time.Time
	var err error
	for _, layout := range tsLayouts {
		ts, err = time.Parse(layout, tsStr)
		if err == nil {
			break
		}
	}
	if err != nil {
		return Entry{}, false
	}

	e := Entry{
		TS:        ts.UTC(),
		Operation: op[1],
		Raw:       line,
		Src:       "unknown",
		User:      "unknown",
		Status:    "unknown",
	}
	if m := statusRe.FindStringSubmatch(line); m != nil && m[1] != "" {
		e.Status = strings.ToLower(m[1])
	}
	if m := userRe.FindStringSubmatch(line); m != nil && m[1] != "" {
		e.User = m[1]
	} else if m := sdUserRe.FindStringSubmatch(line); m != nil && m[1] != "" {
		e.User = m[1]
	}
	if m := srcRe.FindStringSubmatch(line); m != nil {
		e.Src = m[1]
	}
	return e, true
}

// parseAll parses every line of a raw log dump, skipping non-audit lines.
func parseAll(raw string) []Entry {
	lines := strings.Split(raw, "\n")
	entries := make([]Entry, 0, len(lines))
	for _, ln := range lines {
		if e, ok := ParseLine(ln); ok {
			entries = append(entries, e)
		}
	}
	return entries
}
