package spec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Store is the on-disk collection of specs keyed by fingerprint.
type Store struct {
	specs map[string]Spec
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{specs: map[string]Spec{}}
}

// Load reads templates.json. A missing file is treated as an empty store so
// first-run works without ceremony.
func Load(path string) (*Store, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewStore(), nil
		}
		return nil, fmt.Errorf("spec: read %s: %w", path, err)
	}
	s := NewStore()
	if len(bytes.TrimSpace(b)) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(b, &s.specs); err != nil {
		return nil, fmt.Errorf("spec: decode %s: %w", path, err)
	}
	return s, nil
}

// Save writes the store to path with stable key ordering and two-space
// indent so diffs stay reviewable.
func (s *Store) Save(path string) error {
	keys := make([]string, 0, len(s.specs))
	for k := range s.specs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteString("{")
	if len(keys) > 0 {
		buf.WriteString("\n")
	}
	for i, k := range keys {
		keyJSON, _ := json.Marshal(k)
		valJSON, err := json.MarshalIndent(s.specs[k], "  ", "  ")
		if err != nil {
			return fmt.Errorf("spec: encode %s: %w", k, err)
		}
		fmt.Fprintf(&buf, "  %s: %s", keyJSON, valJSON)
		if i < len(keys)-1 {
			buf.WriteString(",")
		}
		buf.WriteString("\n")
	}
	buf.WriteString("}\n")
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// Get returns the spec for a fingerprint and whether one exists.
func (s *Store) Get(fp string) (Spec, bool) {
	sp, ok := s.specs[fp]
	return sp, ok
}

// Put stores a spec. Callers must Validate first.
func (s *Store) Put(sp Spec) {
	s.specs[sp.Fingerprint] = sp
}

// Len returns the number of specs.
func (s *Store) Len() int { return len(s.specs) }

// Fingerprints returns all fingerprints in sorted order.
func (s *Store) Fingerprints() []string {
	out := make([]string, 0, len(s.specs))
	for k := range s.specs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
