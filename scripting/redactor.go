package scripting

import (
	"sort"
	"strings"
	"sync"
)

// Redactor is invocation-local and may be shared by consecutive actions and
// command-output handling. Its zero value is ready to use.
type Redactor struct {
	mu     sync.RWMutex
	values map[string]struct{}
}

// Add must be called before an acquired secret can escape a host operation,
// including when that operation subsequently fails.
func (r *Redactor) Add(values ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values == nil {
		r.values = make(map[string]struct{})
	}
	for _, value := range values {
		if value != "" {
			r.values[value] = struct{}{}
		}
	}
}

func (r *Redactor) Redact(text string) string {
	r.mu.RLock()
	values := make([]string, 0, len(r.values))
	for value := range r.values {
		values = append(values, value)
	}
	r.mu.RUnlock()
	sort.Slice(values, func(i, j int) bool {
		if len(values[i]) == len(values[j]) {
			return values[i] < values[j]
		}
		return len(values[i]) > len(values[j])
	})
	pairs := make([]string, 0, len(values)*2)
	for _, value := range values {
		pairs = append(pairs, value, "[REDACTED]")
	}
	return strings.NewReplacer(pairs...).Replace(text)
}

func safeText(redactor *Redactor, text string, limit int) string {
	text = redactor.Redact(text)
	if len(text) > limit {
		const marker = "\n[truncated]"
		return text[:limit-len(marker)] + marker
	}
	return text
}
