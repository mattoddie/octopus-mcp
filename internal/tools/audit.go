package tools

// The audit log (OCTOPUS_AUDIT_LOG): one JSON line for every call of a tool
// that changes things at Octopus or in the home, so there is a record of what
// the assistant did. Read-only calls are not logged.

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/mattoddie/octopus-mcp/internal/config"
)

// auditLog writes one JSON line per call of a tool that changes things.
type auditLog struct {
	target string
	stderr io.Writer
	now    func() time.Time
	mu     sync.Mutex
	warned sync.Once
}

func newAuditLog(target string) *auditLog {
	if target == "" {
		return nil
	}
	return &auditLog{target: target, stderr: os.Stderr, now: time.Now}
}

// auditEntry is one line of the log. Field order is the order in the file.
type auditEntry struct {
	Time      string `json:"time"`
	Tool      string `json:"tool"`
	Arguments any    `json:"arguments"`
	Outcome   string `json:"outcome"`
	Error     string `json:"error,omitempty"`
}

// record logs one call. A failure to write never fails the tool call: the
// server warns once on standard error and carries on.
func (l *auditLog) record(tool string, args any, callErr error) {
	defer func() {
		if r := recover(); r != nil {
			l.warn(r)
		}
	}()
	entry := auditEntry{
		Time:      l.now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Tool:      tool,
		Arguments: redact(args),
		Outcome:   "ok",
	}
	if callErr != nil {
		entry.Outcome, entry.Error = "error", callErr.Error()
	}
	line, err := json.Marshal(entry)
	if err != nil {
		l.warn(err)
		return
	}
	l.write(append(line, '\n'))
}

func (l *auditLog) write(line []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var err error
	if l.target == config.AuditLogStderr {
		_, err = l.stderr.Write(line)
	} else {
		var f *os.File
		// Opened for each entry, so a rotated or deleted log is recreated.
		if f, err = config.OpenAuditLog(l.target); err == nil {
			_, err = f.Write(line)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
	}
	if err != nil {
		l.warn(err)
	}
}

// warn reports the first audit log failure; tool calls carry on regardless.
func (l *auditLog) warn(err any) {
	l.warned.Do(func() {
		log.Printf("octopus-mcp: WARNING: could not write the audit log (%s): %v. Tool calls carry on; this warning is not repeated.", l.target, err)
	})
}

// secretKey matches argument names whose values must not be logged, such as
// GraphQL variables called password, apiKey or refreshToken.
var secretKey = regexp.MustCompile(`(?i)token|secret|api_?key|password|authori[sz]ation`)

// redact returns the tool's arguments as plain JSON values, with the values
// of secret-looking keys replaced at any depth.
func redact(args any) any {
	b, err := json.Marshal(args)
	if err != nil {
		return nil
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil
	}
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				if secretKey.MatchString(k) {
					x[k] = "[redacted]"
				} else {
					x[k] = walk(val)
				}
			}
		case []any:
			for i := range x {
				x[i] = walk(x[i])
			}
		}
		return v
	}
	return walk(v)
}
