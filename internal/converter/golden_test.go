package converter

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Pass -update to regenerate golden .lua / .merged.lua files from the current
// converter output.
//
//	go test ./internal/converter -run TestGolden -update
var updateGolden = flag.Bool("update", false, "regenerate testdata/*.lua and *.merged.lua golden files")

// TestGolden walks testdata/*.conf and compares each Convert(src) result
// against its golden, in both supported emission modes:
//
//	{name}.conf        →  {name}.lua          (default: MergeCalls=false)
//	{name}.conf        →  {name}.merged.lua   (MergeCalls=true)
//
// MergeCalls=true is the production default for the CLI and the WASM build,
// so it gets its own golden — a missing .merged.lua is a test failure unless
// -update is set.
//
// As a separate, language-level guard, every emitted golden is also fed
// through `luac -p` when a Lua compiler is available on PATH. This catches
// the class of bug where the converter happens to produce text that passes
// a Go-level string match but isn't valid Lua (e.g. a stray '#' comment
// outside line 1, which Lua rejects with "unexpected symbol near '#'").
func TestGolden(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	luac := lookLuaSyntaxChecker()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".conf") {
			continue
		}
		name := e.Name()
		base := strings.TrimSuffix(name, ".conf")
		srcPath := filepath.Join("testdata", name)
		src, err := os.ReadFile(srcPath)
		if err != nil {
			t.Fatalf("read source %s: %v", srcPath, err)
		}
		cases := []struct {
			label    string
			opts     Options
			goldFile string
		}{
			// `default` is the package-zero baseline — strict, no merge,
			// no polyfill — so the golden documents exactly what a bare
			// embedder gets without opting into anything.
			{"default", Options{}, filepath.Join("testdata", base+".lua")},
			// `merged` mirrors the CLI / web-UI production defaults:
			// MergeCalls=true and Polyfill=true. Anything a typical user
			// will see in their generated file is captured here.
			{"merged", Options{MergeCalls: true, Polyfill: true}, filepath.Join("testdata", base+".merged.lua")},
		}
		for _, tc := range cases {
			t.Run(base+"/"+tc.label, func(t *testing.T) {
				got, _, err := ConvertWithOptions(string(src), tc.opts)
				if err != nil {
					t.Fatalf("Convert: %v", err)
				}
				if *updateGolden {
					if err := os.WriteFile(tc.goldFile, []byte(got), 0644); err != nil {
						t.Fatalf("write golden: %v", err)
					}
					return
				}
				want, err := os.ReadFile(tc.goldFile)
				if err != nil {
					t.Fatalf("missing golden %q (run with -update): %v", tc.goldFile, err)
				}
				if string(want) != got {
					t.Errorf("golden mismatch for %s [%s].\nDiff hint: run `go test ./internal/converter -run TestGolden -update` to refresh after intentional changes.\n\n--- want ---\n%s\n--- got ---\n%s", name, tc.label, want, got)
				}
				if luac != "" {
					checkLuaSyntax(t, luac, tc.goldFile)
				}
			})
		}
	}
}

// lookLuaSyntaxChecker returns the absolute path to a Lua syntax checker
// (luac -p) if one is on PATH, or "" if not. luac is a build dep of Lua;
// when absent the test still passes — the syntax check is best-effort.
func lookLuaSyntaxChecker() string {
	for _, name := range []string{"luac", "luac5.4", "luac5.3", "luac5.1"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// checkLuaSyntax fails the test if the file isn't syntactically valid Lua.
// The converter's whole purpose is to produce Lua that Hyprland can load;
// any output that doesn't parse is a bug regardless of whether it matches
// the golden bytes.
func checkLuaSyntax(t *testing.T, luac, path string) {
	t.Helper()
	out, err := exec.Command(luac, "-p", path).CombinedOutput()
	if err != nil {
		t.Errorf("%s: not valid Lua:\n%s", path, strings.TrimSpace(string(out)))
	}
}
