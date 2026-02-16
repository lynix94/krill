package storage

import "sync"

// StringPool provides string interning to reduce memory usage
// by ensuring identical strings share the same underlying memory
type StringPool struct {
	mu   sync.RWMutex
	pool map[string]string
}

// NewStringPool creates a new string pool
func NewStringPool() *StringPool {
	return &StringPool{
		pool: make(map[string]string),
	}
}

// Intern returns a canonical representation of the string
// If the string already exists in the pool, returns the existing instance
// Otherwise, adds it to the pool and returns it
func (sp *StringPool) Intern(s string) string {
	// Fast path: read lock only
	sp.mu.RLock()
	if existing, ok := sp.pool[s]; ok {
		sp.mu.RUnlock()
		return existing
	}
	sp.mu.RUnlock()

	// Slow path: write lock
	sp.mu.Lock()
	defer sp.mu.Unlock()

	// Double-check after acquiring write lock (another goroutine might have added it)
	if existing, ok := sp.pool[s]; ok {
		return existing
	}

	// Add to pool
	sp.pool[s] = s
	return s
}

// Size returns the number of unique strings in the pool
func (sp *StringPool) Size() int {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return len(sp.pool)
}

// Clear removes all strings from the pool
// Use with caution - only safe when no references to interned strings exist
func (sp *StringPool) Clear() {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.pool = make(map[string]string)
}

// GlobalStringPool is a global string pool instance
// Used by default for label name/value interning
var GlobalStringPool = NewStringPool()

// InternLabel creates a new Label with interned strings
func InternLabel(name, value string) Label {
	return Label{
		Name:  GlobalStringPool.Intern(name),
		Value: GlobalStringPool.Intern(value),
	}
}

// InternLabels creates Labels with all strings interned
func InternLabels(labels Labels) Labels {
	interned := make(Labels, len(labels))
	for i, l := range labels {
		interned[i] = InternLabel(l.Name, l.Value)
	}
	return interned
}
