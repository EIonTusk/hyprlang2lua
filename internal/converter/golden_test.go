package converter

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pass -update to regenerate golden .lua files from the current converter output.
//   go test ./internal/converter -run TestGolden -update
var updateGolden = flag.Bool("update", false, "regenerate testdata/*.lua golden files")

// TestGolden walks testdata/*.conf and compares each Convert(src) result
// against the sibling .lua golden. A missing golden is regenerated under
// -update; otherwise its absence is a test failure.
func TestGolden(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".conf") {
			continue
		}
		name := e.Name()
		t.Run(strings.TrimSuffix(name, ".conf"), func(t *testing.T) {
			srcPath := filepath.Join("testdata", name)
			goldPath := strings.TrimSuffix(srcPath, ".conf") + ".lua"

			src, err := os.ReadFile(srcPath)
			if err != nil {
				t.Fatalf("read source: %v", err)
			}
			got, _, err := Convert(string(src))
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			if *updateGolden {
				if err := os.WriteFile(goldPath, []byte(got), 0644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(goldPath)
			if err != nil {
				t.Fatalf("missing golden %q (run with -update): %v", goldPath, err)
			}
			if string(want) != got {
				t.Errorf("golden mismatch for %s.\nDiff hint: run `go test ./internal/converter -run TestGolden -update` to refresh after intentional changes.\n\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
			}
		})
	}
}
