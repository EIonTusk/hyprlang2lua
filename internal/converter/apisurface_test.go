package converter

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// recorderLua is a harness that loads a converter-generated file with a fake
// `hl` table and prints, one per line, the API surface the file actually
// touched:
//
//	CFG <dotted.config.key>     — a key that resolved against the registry
//	BAD <dotted.config.key>     — a key Hyprland would reject as unknown
//	OPT <bind_option_field>     — an HL.BindOptions field passed to hl.bind
//	SPC <HL.XSpec>.<field>      — a top-level key passed to a flat hl.* spec
//
// This validates the real artifact the same way Hyprland does — by executing
// it — rather than string-matching the generator's output. `hl` is a permissive
// metatable so arbitrarily deep accesses (hl.dsp.window.resize, hl.dsp.cursor
// .move_to_corner, …) resolve to callable no-ops and the whole file runs to
// completion regardless of which dispatchers it uses.
//
// walk() deliberately mirrors Hyprland's own hlConfig loop
// (src/config/lua/bindings/LuaBindingsConfigRules.cpp): look the accumulated
// dotted path up in the registry first, and only recurse when it is *not* a
// known key. That distinction matters — a known key whose value is a table is
// a typed leaf (HL.CssGap `gaps_in = { top = …, right = … }`, a gradient's
// `{ colors = {…} }`), not a nested section. Classifying by table shape
// instead would misread those as `general.gaps_in.top`.
const recorderLua = `
local out = {}
local KEYS = dofile(KEYSFILE)

-- A callable, infinitely-indexable no-op. Covers every hl.dsp.* namespace and
-- any hl.* function whose arguments we don't care about here.
local function stub()
  local t
  t = setmetatable({}, {
    __index = function() return stub() end,
    __call  = function() return t end,
  })
  return t
end

-- Generated files 'require' sibling modules for each converted 'source ='
-- directive. Those modules aren't on package.path here (and their contents are
-- covered by their own goldens), so resolve them to no-ops.
local realRequire = require
require = function(name)
  local ok, mod = pcall(realRequire, name)
  if ok then return mod end
  return stub()
end

local function walk(prefix, tbl)
  for k, v in pairs(tbl) do
    if type(k) == "string" then
      local full = prefix == "" and k or (prefix .. "." .. k)
      if KEYS[full] then
        out[#out+1] = "CFG " .. full
      elseif type(v) == "table" then
        walk(full, v)
      else
        out[#out+1] = "BAD " .. full
      end
    end
  end
end

-- Flat spec tables whose declared field set is complete, so a top-level
-- membership check is sound. window_rule/layer_rule are excluded: their effect
-- keys are siblings that HL.WindowRuleSpec/HL.LayerRuleSpec don't declare.
local SPECS = {
  permission     = "HL.PermissionSpec",
  monitor        = "HL.MonitorSpec",
  device         = "HL.DeviceSpec",
  gesture        = "HL.GestureSpec",
  workspace_rule = "HL.WorkspaceRuleSpec",
}

local recorded = {
  config = function(t) if type(t) == "table" then walk("", t) end end,
  bind = function(_, _, opts)
    if type(opts) == "table" then
      for k, _ in pairs(opts) do
        if type(k) == "string" then out[#out+1] = "OPT " .. k end
      end
    end
  end,
}

for fn, class in pairs(SPECS) do
  recorded[fn] = function(spec)
    if type(spec) == "table" then
      for k, _ in pairs(spec) do
        if type(k) == "string" then out[#out+1] = "SPC " .. class .. "." .. k end
      end
    end
    return stub() -- 0.56's hl.workspace_rule returns a handle; stay chainable
  end
end

hl = setmetatable(recorded, {
  __index = function() return stub() end,
})

local chunk, err = loadfile(FILE)
if not chunk then io.stderr:write("load error: " .. tostring(err) .. "\n"); os.exit(1) end
local ok, rerr = pcall(chunk)
if not ok then io.stderr:write("run error: " .. tostring(rerr) .. "\n"); os.exit(1) end

for _, line in ipairs(out) do print(line) end
`

// TestAPISurface executes every golden output under a recording `hl` stub and
// checks each config key and bind option it touches against the API surface
// pinned in testdata/api/ (extracted from Hyprland v0.56.1's generated
// hl.meta.lua).
//
// This closes a gap that neither the golden bytes nor `luac -p` can reach:
// output that is valid Lua and matches its golden exactly, but names a config
// key Hyprland doesn't have. Hyprland's hlConfig looks each dotted path up
// verbatim and reports "unknown config key" — a load-time error invisible to
// any syntax-level check. The hyphenated-route bug (emitting
// input.touchpad['tap-to-click'] where the registry has tap_to_click) was
// exactly this shape.
//
// Scope limit worth knowing: the generated stubs type every dispatcher as
// `fun(...): HL.Dispatcher`, so dispatcher argument tables (resize's
// keep_aspect_ratio, move's into_or_create_group, …) carry no field
// information and cannot be checked this way. Those still rest on reading
// Hyprland's LuaBindingsDispatchers.cpp.
//
// Skipped when no Lua interpreter is on PATH, matching TestGolden's
// best-effort `luac -p` gate.
func TestAPISurface(t *testing.T) {
	lua := lookLuaInterpreter()
	if lua == "" {
		t.Skip("no lua interpreter on PATH; skipping API surface check")
	}
	configKeys := readPinnedSet(t, filepath.Join("testdata", "api", "config_keys.txt"))
	bindOpts := readPinnedSet(t, filepath.Join("testdata", "api", "bind_options.txt"))
	specFields := readPinnedSet(t, filepath.Join("testdata", "api", "spec_fields.txt"))

	dir := t.TempDir()
	harness := filepath.Join(dir, "recorder.lua")
	if err := os.WriteFile(harness, []byte(recorderLua), 0644); err != nil {
		t.Fatalf("write harness: %v", err)
	}
	// The harness resolves keys the way Hyprland does, so it needs the registry.
	keysFile := filepath.Join(dir, "keys.lua")
	if err := os.WriteFile(keysFile, []byte(luaKeySetLiteral(configKeys)), 0644); err != nil {
		t.Fatalf("write keys: %v", err)
	}

	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".lua") {
			continue
		}
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			target, err := filepath.Abs(filepath.Join("testdata", name))
			if err != nil {
				t.Fatalf("abs: %v", err)
			}
			// -e pre-sets FILE/KEYSFILE so the harness needs no arg plumbing.
			preamble := "FILE=" + quoteLuaString(target) + ";KEYSFILE=" + quoteLuaString(keysFile)
			cmd := exec.Command(lua, "-e", preamble, harness)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("lua harness failed on %s: %v\n%s", name, err, out)
			}
			var badKeys, badOpts, badSpec []string
			resolved := 0
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				kind, val, ok := strings.Cut(line, " ")
				if !ok {
					continue
				}
				switch kind {
				case "CFG":
					resolved++
				case "BAD":
					badKeys = append(badKeys, val)
				case "OPT":
					if !bindOpts[val] {
						badOpts = append(badOpts, val)
					}
				case "SPC":
					if !specFields[val] {
						badSpec = append(badSpec, val)
					}
				}
			}
			// Guard against a vacuous pass: every golden derived from a .conf
			// with a config section must resolve at least one real key.
			if resolved == 0 && len(badKeys) == 0 && strings.Contains(readFileString(t, target), "hl.config({") {
				t.Errorf("%s: calls hl.config but the harness recorded no keys — harness is not observing anything", name)
			}
			for _, k := range dedupeSorted(badKeys) {
				t.Errorf("%s: emitted config key %q is not in Hyprland's registry — hl.config would reject it as an unknown config key", name, k)
			}
			for _, o := range dedupeSorted(badOpts) {
				t.Errorf("%s: emitted bind option %q is not a field on HL.BindOptions", name, o)
			}
			for _, s := range dedupeSorted(badSpec) {
				// Split on the LAST dot: the class name itself contains one
				// ("HL.PermissionSpec.allow" → HL.PermissionSpec / allow).
				class, field := s, ""
				if i := strings.LastIndex(s, "."); i >= 0 {
					class, field = s[:i], s[i+1:]
				}
				t.Errorf("%s: emitted %q, which %s does not declare — the call would be rejected at config load", name, field, class)
			}
		})
	}
}

// TestAPISurfaceCatchesBadKeys guards the guard: a config key with the
// hyphenated source spelling must be reported as unknown. Without this, a
// harness that silently recorded nothing would let TestAPISurface pass
// vacuously.
func TestAPISurfaceCatchesBadKeys(t *testing.T) {
	keys := readPinnedSet(t, filepath.Join("testdata", "api", "config_keys.txt"))
	for _, good := range []string{
		"input.touchpad.tap_to_click",
		"input_capture.capture_modifiers",
		"decoration.motion_blur.enabled",
		"misc.session_lock_blur",
	} {
		if !keys[good] {
			t.Errorf("pinned set is missing %q — regenerate testdata/api/config_keys.txt", good)
		}
	}
	for _, bad := range []string{
		"input.touchpad.tap-to-click",
		"input-capture.capture_modifiers",
		"input_capture.capture-modifiers",
	} {
		if keys[bad] {
			t.Errorf("pinned set unexpectedly contains the hyphenated spelling %q", bad)
		}
	}
}

func lookLuaInterpreter() string {
	for _, name := range []string{"lua", "lua5.4", "lua5.3", "lua5.1", "luajit"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// readPinnedSet loads one testdata/api/*.txt list, skipping '#' comments and
// blank lines.
func readPinnedSet(t *testing.T, path string) map[string]bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	set := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		set[line] = true
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	if len(set) == 0 {
		t.Fatalf("%s is empty", path)
	}
	return set
}

// luaKeySetLiteral renders the registry as a Lua chunk returning a set table,
// so the harness can resolve keys exactly the way Hyprland's hlConfig does.
func luaKeySetLiteral(keys map[string]bool) string {
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	var b strings.Builder
	b.WriteString("return {\n")
	for _, k := range sorted {
		b.WriteString("  [" + quoteLuaString(k) + "] = true,\n")
	}
	b.WriteString("}\n")
	return b.String()
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func dedupeSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// TestHyprlandVerifyConfig runs every golden through a real Hyprland binary's
// `--verify-config`, which loads the Lua config and reports errors without
// starting a compositor. This is the highest-fidelity gate available: it
// validates config keys, spec table fields, dispatcher argument tables, and
// rule effects all at once, against the actual implementation rather than a
// model of it.
//
// It found the hl.permission `allow=` → `mode=` bug that the goldens, `luac -p`
// and the pinned-surface check all passed.
//
// Opt-in via HYPRLAND_BIN — deliberately NOT auto-discovered from PATH:
//
//	HYPRLAND_BIN=/path/to/Hyprland go test ./internal/converter -run VerifyConfig
//
// Auto-discovery would make `go test ./...` fail for anyone whose installed
// Hyprland predates testdata/api/HYPRLAND_VERSION, since older releases
// legitimately lack the newer keys and dispatchers the goldens exercise (0.55.4,
// for instance, has no hl.dsp.release_input_capture at all). For the same
// reason this skips unless the binary's major.minor matches the pinned target —
// a mismatch is a stale toolchain, not a converter bug.
func TestHyprlandVerifyConfig(t *testing.T) {
	bin := os.Getenv("HYPRLAND_BIN")
	if bin == "" {
		t.Skip("HYPRLAND_BIN not set; skipping real config verification")
	}
	want := pinnedHyprlandMinor(t)
	got, err := hyprlandMinor(bin)
	if err != nil {
		t.Skipf("could not determine %s version (%v); skipping", bin, err)
	}
	if got != want {
		t.Skipf("HYPRLAND_BIN is %s but testdata/api is pinned to %s.x — skipping to avoid spurious failures", got, want)
	}

	// Copy goldens somewhere writable and satisfy the `require`s that converted
	// `source =` directives emit, so a missing sibling module doesn't masquerade
	// as a config error.
	dir := t.TempDir()
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	var goldens []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".lua") {
			continue
		}
		src := readFileString(t, filepath.Join("testdata", e.Name()))
		dst := filepath.Join(dir, e.Name())
		if err := os.WriteFile(dst, []byte(src), 0644); err != nil {
			t.Fatalf("write %s: %v", dst, err)
		}
		goldens = append(goldens, dst)
		for _, mod := range requiredModules(src) {
			stub := filepath.Join(dir, mod+".lua")
			if _, err := os.Stat(stub); err == nil {
				continue
			}
			if err := os.WriteFile(stub, []byte("return {}\n"), 0644); err != nil {
				t.Fatalf("write module stub %s: %v", stub, err)
			}
		}
	}

	for _, g := range goldens {
		t.Run(filepath.Base(g), func(t *testing.T) {
			out, _ := exec.Command(bin, "--verify-config", "-c", g).CombinedOutput()
			// The banner precedes the verdict; everything after it is either
			// "config ok" or one error per line.
			_, verdict, found := strings.Cut(string(out), "Config parsing result:")
			if !found {
				t.Fatalf("unexpected --verify-config output:\n%s", out)
			}
			verdict = strings.TrimSpace(verdict)
			if verdict != "config ok" {
				t.Errorf("Hyprland rejected the generated config:\n%s", verdict)
			}
		})
	}
}

// pinnedHyprlandMinor returns the "major.minor" of the release that
// testdata/api/ was extracted from.
func pinnedHyprlandMinor(t *testing.T) string {
	t.Helper()
	for line := range strings.SplitSeq(readFileString(t, filepath.Join("testdata", "api", "HYPRLAND_VERSION")), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return majorMinor(line)
	}
	t.Fatal("testdata/api/HYPRLAND_VERSION has no version line")
	return ""
}

// hyprlandMinor asks the binary for its version and reduces it to major.minor.
func hyprlandMinor(bin string) (string, error) {
	out, err := exec.Command(bin, "--version-json").Output()
	if err != nil {
		return "", err
	}
	var v struct{ Version string }
	if err := json.Unmarshal(out, &v); err != nil {
		return "", err
	}
	if v.Version == "" {
		return "", errors.New(`no "version" field in --version-json output`)
	}
	return majorMinor(v.Version), nil
}

func majorMinor(v string) string {
	parts := strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3)
	if len(parts) < 2 {
		return v
	}
	return parts[0] + "." + parts[1]
}

// requiredModules extracts the module names from `require("x")` calls, which
// the converter emits for each `source =` directive.
func requiredModules(src string) []string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, `require("`)
		if !ok {
			continue
		}
		if name, ok := strings.CutSuffix(rest, `")`); ok && name != "" {
			out = append(out, name)
		}
	}
	return out
}
