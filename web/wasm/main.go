// Wasm entry point. Exposes window.hyprlang2lua = {
//   convert(src: string, opts?: { mergeCalls?: boolean, stripComments?: boolean, hoistVariables?: boolean })
//     -> {lua: string, translated: int, passthrough: int,
//         flagged: int, coverage: number, notes: array,
//         lineClass: { [line: number]: "translated"|"flagged"|"passthrough" },
//         error: string|null}
// }
//
// `mergeCalls` defaults to true (matches CLI). The legacy `mergeConfig` key
// is still accepted for back-compat.
//
// Build:
//   GOOS=js GOARCH=wasm go build -o ../main.wasm .
//
//go:build js && wasm

package main

import (
	"strconv"
	"syscall/js"

	"github.com/EIonTusk/hyprlang2lua/internal/converter"
)

func main() {
	api := js.Global().Get("Object").New()
	api.Set("convert", js.FuncOf(convert))
	js.Global().Set("hyprlang2lua", api)
	// Block forever — js callers reach us through the exported function.
	select {}
}

func convert(this js.Value, args []js.Value) any {
	result := js.Global().Get("Object").New()
	if len(args) < 1 || args[0].Type() != js.TypeString {
		result.Set("error", "convert(src, opts?) expects (string, object?)")
		return result
	}
	// Defaults match the CLI: merge ON, the rest OFF. JS callers can
	// override either explicitly. The legacy `mergeConfig` key is accepted
	// for back-compat with anyone still embedding the older wasm contract.
	opts := converter.Options{MergeCalls: true}
	if len(args) >= 2 && args[1].Type() == js.TypeObject {
		if v := args[1].Get("mergeCalls"); v.Type() == js.TypeBoolean {
			opts.MergeCalls = v.Bool()
		} else if v := args[1].Get("mergeConfig"); v.Type() == js.TypeBoolean {
			opts.MergeCalls = v.Bool()
		}
		if v := args[1].Get("stripComments"); v.Type() == js.TypeBoolean {
			opts.StripComments = v.Bool()
		}
		if v := args[1].Get("hoistVariables"); v.Type() == js.TypeBoolean {
			opts.HoistVariables = v.Bool()
		}
	}
	lua, rpt, err := converter.ConvertWithOptions(args[0].String(), opts)
	if err != nil {
		result.Set("error", err.Error())
		return result
	}
	result.Set("error", nil)
	result.Set("lua", lua)
	result.Set("translated", rpt.Translated)
	result.Set("passthrough", rpt.Passthrough)
	result.Set("flagged", rpt.Flagged)
	result.Set("coverage", rpt.CoveragePct())
	notes := js.Global().Get("Array").New()
	for i, n := range rpt.Notes {
		entry := js.Global().Get("Object").New()
		entry.Set("line", n.Line)
		entry.Set("text", n.Text)
		notes.SetIndex(i, entry)
	}
	result.Set("notes", notes)

	lineClass := js.Global().Get("Object").New()
	for line, class := range rpt.LineClass {
		// Keys must be strings on JS Objects.
		lineClass.Set(strconv.Itoa(line), class)
	}
	result.Set("lineClass", lineClass)
	return result
}
