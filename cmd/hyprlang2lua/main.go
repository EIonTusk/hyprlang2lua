// hyprlang2lua converts a hyprlang (.conf) file to Hyprland 0.55+ Lua.
//
//	hyprlang2lua input.conf > output.lua
//	hyprlang2lua --dir ~/.config/hypr      # walk a tree, writing *.lua next to each *.conf
//	hyprlang2lua --report input.conf       # print conversion stats to stderr
//	hyprlang2lua --check input.conf        # exit non-zero if any line was flagged for manual review
//
// Standard library only.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	flag "github.com/spf13/pflag"

	"github.com/EIonTusk/hyprlang2lua/internal/converter"
)

func main() {
	var (
		dir       string
		report    bool
		check     bool
		inplace   bool
		noMerge   bool
		strip     bool
		hoistVars bool
		outPath   string
	)
	fs := flag.NewFlagSet("hyprlang2lua", flag.ContinueOnError)
	fs.StringVarP(&dir, "dir", "d", "", "walk a `DIR`ectory tree, converting every *.conf to a sibling *.lua")
	fs.BoolVarP(&report, "report", "r", false, "print a coverage report to stderr after conversion")
	fs.BoolVarP(&check, "check", "c", false, "exit non-zero if any line was flagged for manual review (use in CI)")
	fs.BoolVar(&inplace, "in-place", false, "with --dir, overwrite existing .lua siblings")
	fs.BoolVar(&noMerge, "no-merge", false, "emit a separate hl.X(...) call per source line instead of merging mergeable APIs (currently hl.config) into one call")
	fs.BoolVarP(&strip, "strip-comments", "s", false, "drop comments from the output (TODO markers from flagged directives are kept)")
	fs.BoolVar(&hoistVars, "hoist-vars", false, "move every '$var = ...' rewrite into a single block at the top of the output")
	fs.StringVarP(&outPath, "out", "o", "", "write output to `FILE` instead of stdout (single-file mode)")

	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `usage: hyprlang2lua [flags] [input.conf]
       hyprlang2lua --dir DIR [flags]

Converts hyprlang (.conf) configuration to the Hyprland 0.55+ Lua format.
Reads from stdin when no input path is given.

flags:
`)
		fmt.Fprint(os.Stderr, fs.FlagUsages())
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	opts := converter.Options{MergeCalls: !noMerge, StripComments: strip, HoistVariables: hoistVars}

	if dir != "" {
		flagged, err := convertDir(dir, inplace, report, opts)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if check && flagged > 0 {
			os.Exit(3)
		}
		return
	}

	var src []byte
	switch fs.NArg() {
	case 0:
		// If stdin is a terminal (i.e. nothing piped in), the user almost
		// certainly invoked the bare command by mistake — print help and
		// exit rather than hanging on a read.
		if isTerminal(os.Stdin) {
			fs.Usage()
			os.Exit(2)
		}
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error reading stdin:", err)
			os.Exit(1)
		}
		src = b
	case 1:
		b, err := os.ReadFile(fs.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "error reading file:", err)
			os.Exit(1)
		}
		src = b
	default:
		fs.Usage()
		os.Exit(2)
	}

	lua, rpt, err := converter.ConvertWithOptions(string(src), opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "convert error:", err)
		os.Exit(1)
	}

	var writer io.Writer = os.Stdout
	if outPath != "" {
		f, err := os.Create(outPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error opening output:", err)
			os.Exit(1)
		}
		defer f.Close()
		writer = f
	}
	if _, err := io.WriteString(writer, lua); err != nil {
		fmt.Fprintln(os.Stderr, "error writing output:", err)
		os.Exit(1)
	}

	if report {
		printReport(os.Stderr, "stdin", rpt)
	}
	if check && rpt.Flagged > 0 {
		os.Exit(3)
	}
}

// convertDir walks 'root', converting every *.conf to a sibling *.lua.
// Returns the total number of flagged directives across all files.
func convertDir(root string, overwrite, report bool, opts converter.Options) (int, error) {
	totalFlagged := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".conf") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lua, rpt, err := converter.ConvertWithOptions(string(src), opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			return nil
		}
		out := strings.TrimSuffix(path, ".conf") + ".lua"
		if _, err := os.Stat(out); err == nil && !overwrite {
			fmt.Fprintf(os.Stderr, "%s exists (use --in-place to overwrite); skipping\n", out)
			return nil
		}
		if err := os.WriteFile(out, []byte(lua), 0644); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "%s -> %s\n", path, out)
		if report {
			printReport(os.Stderr, path, rpt)
		}
		totalFlagged += rpt.Flagged
		return nil
	})
	return totalFlagged, err
}

// isTerminal reports whether f refers to a character device (a TTY) rather
// than a pipe, redirect, or regular file. Standard-library only — no x/term.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func printReport(w io.Writer, name string, r converter.Report) {
	pct := r.CoveragePct() * 100
	fmt.Fprintf(w, "[%s] translated=%d passthrough=%d flagged=%d coverage=%.1f%%\n",
		name, r.Translated, r.Passthrough, r.Flagged, pct)
	for _, n := range r.Notes {
		fmt.Fprintf(w, "  line %d: %s\n", n.Line, n.Text)
	}
}
