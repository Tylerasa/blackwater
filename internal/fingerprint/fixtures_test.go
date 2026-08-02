package fingerprint_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/momo-ledger/momo-ledger/internal/fingerprint"
)

type fixture struct {
	Name   string `json:"name"`
	Sender string `json:"sender"`
	Body   string `json:"body"`
}

type fixtureFile struct {
	Records []fixture `json:"records"`
}

// TestFixturesDistinct pins the practical invariant that motivates the whole
// design: every distinct message template in our fixtures must produce a
// distinct fingerprint. If someone weakens a mask and starts collapsing
// unrelated templates, this test screams.
func TestFixturesDistinct(t *testing.T) {
	// walk up from the package dir to repo root, then to testdata
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(wd, "..", "..", "testdata", "fixtures.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("fixtures.json not found at %s: %v", path, err)
	}
	var ff fixtureFile
	if err := json.Unmarshal(b, &ff); err != nil {
		t.Fatalf("decode fixtures: %v", err)
	}
	if len(ff.Records) == 0 {
		t.Fatal("no fixtures loaded")
	}

	seen := map[string]string{} // hash -> fixture name
	for _, r := range ff.Records {
		h, _ := fingerprint.Fingerprint(r.Sender, r.Body)
		if prior, ok := seen[h]; ok {
			t.Errorf("template collision: %q and %q share fingerprint %s",
				prior, r.Name, h[:12])
		}
		seen[h] = r.Name
	}
}
