// Package converter translates hyprlang (.conf) source into the Hyprland 0.55+
// Lua configuration format.
//
// The package is pure — no os, no net, no filesystem. It exposes a single
// entry point [Convert] that takes the source text and returns the generated
// Lua, plus a coverage [Report]. This keeps the CLI thin and lets the same
// code run unchanged under GOOS=js for the web build.
package converter


// Report summarizes what happened during conversion.
//
// Translated  — directives mapped to a real Hyprland Lua API call, plus
//                variable declarations rewritten as 'local foo = "bar"'.
// Passthrough — content preserved verbatim aside from the comment sigil
//                (comments and blank lines).
// Flagged     — emitted with a "-- TODO: manual review" marker because the
//                converter could not safely translate them. --check exits
//                non-zero when this is non-zero.
type Report struct {
	Translated  int
	Passthrough int
	Flagged     int
	Notes       []ReportNote
	// LineClass maps 1-based source-line numbers to a classification used by
	// UIs to paint a diff-style gutter on the input: "translated" (line was
	// mapped to a real hl.* call, to a config table key, or to a `local`
	// variable rewrite), "flagged" (a TODO marker was emitted for that line),
	// or "passthrough" (comment). Lines absent from the map were blank or
	// empty.
	LineClass map[int]string
}

type ReportNote struct {
	Line int
	Text string
}

func (r Report) Total() int { return r.Translated + r.Passthrough + r.Flagged }

// CoveragePct returns translated / (translated + flagged), ignoring passthrough.
// 1.0 means everything semantic was understood. Returns 1.0 on an empty file.
func (r Report) CoveragePct() float64 {
	denom := r.Translated + r.Flagged
	if denom == 0 {
		return 1.0
	}
	return float64(r.Translated) / float64(denom)
}

// Options tunes how the generator emits Lua. The zero value is the default
// "one hl.config({...}) call per section" behavior. User-facing surfaces
// (the CLI binary and the web UI) default MergeCalls to true.
type Options struct {
	// MergeCalls collapses every hl.X(...) call into a single one wherever
	// the underlying Lua API supports it. In practice this is hl.config: all
	// `hl.config({ section = {...} })` calls fold into a single trailing
	// `hl.config({...})` whose top-level keys are the sections in
	// first-seen order. Top-level comments that appeared between sections
	// in the source are preserved as comments inside the merged table so
	// the visual grouping survives. Other hl.* APIs (bind, window_rule,
	// monitor, env, device, …) take a single spec per call by design, so
	// they pass through unchanged — there is no merge analog the engine
	// would accept. The flag is therefore a no-op for those, but turning
	// it on costs nothing and produces the most compact output the API
	// permits.
	MergeCalls bool

	// StripComments drops every Comment node and every comment that would
	// otherwise be emitted inside a config table. TODO markers from flagged
	// directives are still emitted — those carry actionable information.
	StripComments bool

	// HoistVariables moves every '$foo = bar' rewrite to a single block at
	// the top of the output, right after the generated-by header. The body
	// of the file then skips emitting them in place, so references to a
	// variable from anywhere in the config remain valid. Source-position
	// comments around the variables are preserved where they were.
	HoistVariables bool

	// Polyfill emits runtime Lua helper code to preserve hyprlang features
	// whose semantics have no direct equivalent in Hyprland 0.55's typed Lua
	// API. With Polyfill ON it emits a `function() ... end` closure that
	// performs the needed runtime lookups (active window, monitor, etc.)
	// before calling `hl.dispatch(...)`. With Polyfill OFF the converter is
	// strict: any input it can't translate into a single typed API call is
	// flagged. The trade-off: ON preserves more user intent at the cost of
	// slightly larger output and a runtime query per dispatch.
	//
	// User-facing surfaces (the CLI binary and the web UI) default this to
	// true — preserving the user's config wins over output minimalism. The
	// CLI exposes `--no-polyfill` to opt out, matching the `--no-merge`
	// pattern for [MergeCalls]. The zero value remains false so embedders
	// that construct Options directly get strict behavior unless they ask.
	//
	// Currently powers:
	//   - 'resizeactive 10% 5%' / 'moveactive 10% 5%' (and the *windowpixel
	//     variants): percent inputs are rejected outright when OFF (the typed
	//     resize/move table only accepts numeric pixels), and resolved at
	//     dispatch time against the active/target window or monitor when ON.
	// Future hyprlang features that need a runtime lookup should opt in
	// through this same flag rather than introducing one knob per feature.
	Polyfill bool
}

// Convert is the package entry point with default options. See
// [ConvertWithOptions] for the options-aware form.
func Convert(src string) (string, Report, error) {
	return ConvertWithOptions(src, Options{})
}

// ConvertWithOptions runs the lexer, parser, and code generator with the
// supplied [Options]. An error is returned only for hard parse failures;
// soft issues surface as "-- TODO: manual review" lines and Flagged++.
func ConvertWithOptions(src string, opts Options) (string, Report, error) {
	toks, err := newLexer(src).Lex()
	if err != nil {
		return "", Report{}, err
	}
	file, err := parse(toks)
	if err != nil {
		return "", Report{}, err
	}
	g := newGenerator(opts)
	g.preClassify(file.Nodes)
	g.gen(file)
	return collapseBlanks(g.out.String()), g.report, nil
}

// collapseBlanks reduces any run of 3+ newlines to exactly two, so the
// output has at most one blank line between top-level statements.
func collapseBlanks(s string) string {
	out := make([]byte, 0, len(s))
	newlineRun := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' {
			newlineRun++
			if newlineRun <= 2 {
				out = append(out, c)
			}
			continue
		}
		newlineRun = 0
		out = append(out, c)
	}
	return string(out)
}
