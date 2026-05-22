package js

import (
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"crude/logger"

	"github.com/dop251/goja"
)

const (
	maxExecTime = 10 * time.Second
	maxCodeLen  = 50000
)

var (
	pkgLogger   *logger.Logger
	pkgLoggerMu sync.RWMutex
)

func SetLogger(lg *logger.Logger) {
	pkgLoggerMu.Lock()
	defer pkgLoggerMu.Unlock()
	pkgLogger = lg
}

func logIf(fn func(lg *logger.Logger)) {
	pkgLoggerMu.RLock()
	lg := pkgLogger
	pkgLoggerMu.RUnlock()
	if lg != nil {
		fn(lg)
	}
}

// Run executes JavaScript code in a sandboxed goja VM and returns the result.
// The last expression value is returned as a string.
func Run(code string) (string, error) {
	if code == "" {
		return "", fmt.Errorf("js: empty code")
	}
	if len(code) > maxCodeLen {
		return "", fmt.Errorf("js: code too long (%d chars, max %d)", len(code), maxCodeLen)
	}

	logIf(func(lg *logger.Logger) { lg.Info("JS", "run", "code_len", len(code), "timeout", maxExecTime) })
	start := time.Now()

	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))

	// Capture console.log output.
	var consoleOut strings.Builder
	consoleObj := vm.NewObject()
	consoleFormatArg := func(a goja.Value) string {
		if a == nil || goja.IsUndefined(a) || goja.IsNull(a) {
			return "undefined"
		}
		if obj, ok := a.(*goja.Object); ok {
			_ = vm.Set("__crude_console_arg__", obj)
			jsonStr, err := vm.RunString("JSON.stringify(__crude_console_arg__)")
			if err == nil && !goja.IsUndefined(jsonStr) {
				s := jsonStr.String()
				if s != "" && s != "undefined" {
					return s
				}
			}
		}
		return a.String()
	}
	_ = consoleObj.Set("log", func(args ...goja.Value) {
		parts := make([]string, 0, len(args))
		for _, a := range args {
			parts = append(parts, consoleFormatArg(a))
		}
		consoleOut.WriteString(strings.Join(parts, " "))
		consoleOut.WriteByte('\n')
	})
	_ = consoleObj.Set("error", func(args ...goja.Value) {
		parts := make([]string, 0, len(args))
		for _, a := range args {
			parts = append(parts, consoleFormatArg(a))
		}
		consoleOut.WriteString("[error] " + strings.Join(parts, " "))
		consoleOut.WriteByte('\n')
	})
	_ = vm.Set("console", consoleObj)

	// Add Math helpers beyond standard Math.
	_ = vm.Set("sum", func(arr []float64) float64 {
		var s float64
		for _, v := range arr {
			s += v
		}
		return s
	})
	_ = vm.Set("mean", func(arr []float64) float64 {
		if len(arr) == 0 {
			return 0
		}
		var s float64
		for _, v := range arr {
			s += v
		}
		return s / float64(len(arr))
	})
	_ = vm.Set("round", func(v float64, decimals int) float64 {
		p := 1.0
		for i := 0; i < decimals; i++ {
			p *= 10
		}
		return float64(int(v*p+0.5)) / p
	})

	// Run with timeout via channel.
	type result struct {
		value goja.Value
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logIf(func(lg *logger.Logger) { lg.Error("JS", "panic", "code_len", len(code), "error", fmt.Sprintf("%v", r)) })
				ch <- result{nil, fmt.Errorf("js: panic: %v", r)}
			}
		}()
		val, err := vm.RunString(code)
		ch <- result{val, err}
	}()

	var val goja.Value
	var err error
	select {
	case r := <-ch:
		val, err = r.value, r.err
	case <-time.After(maxExecTime):
		logIf(func(lg *logger.Logger) { lg.Warn("JS", "timeout", "code_len", len(code), "timeout", maxExecTime) })
		return "", fmt.Errorf("js: execution timed out (%s)", maxExecTime)
	}

	if err != nil {
		logIf(func(lg *logger.Logger) { lg.Warn("JS", "error", "code_len", len(code), "error", err.Error(), "latency_ms", time.Since(start).Milliseconds()) })
		return formatJSError(err, consoleOut.String()), nil
	}

	var sb strings.Builder

	if consoleOut.Len() > 0 {
		sb.WriteString("Console output:\n")
		sb.WriteString(consoleOut.String())
		sb.WriteByte('\n')
	}

	if val != nil && !goja.IsUndefined(val) && !goja.IsNull(val) {
		sb.WriteString("Result: ")
		resultStr := formatValue(vm, val)
		sb.WriteString(resultStr)
	}

	out := sb.String()
	logIf(func(lg *logger.Logger) { lg.Info("JS", "result", "code_len", len(code), "output_len", len(out), "latency_ms", time.Since(start).Milliseconds()) })
	if out == "" {
		return "(no output)", nil
	}
	return out, nil
}

func formatValue(vm *goja.Runtime, val goja.Value) string {
	if val == nil || goja.IsUndefined(val) || goja.IsNull(val) {
		return "undefined"
	}

	if obj, ok := val.(*goja.Object); ok {
		_ = vm.Set("__crude_json_val__", obj)

		_, _ = vm.RunString(`__crude_replacer__ = function(key, value) {
			if (typeof value === 'function') return '[Function]';
			if (value instanceof RegExp) return value.toString();
			if (value instanceof Map) return Object.fromEntries(value);
			if (value instanceof Set) return [...value];
			return value;
		}`)

		jsonStr, err := vm.RunString("JSON.stringify(__crude_json_val__, __crude_replacer__, 2)")
		if err == nil && !goja.IsUndefined(jsonStr) {
			s := jsonStr.String()
			if s != "undefined" && s != "" {
				return s
			}
		}

		desc, descErr := vm.RunString(`(function() {
			var v = __crude_json_val__;
			var name = (v.constructor && v.constructor.name) ? v.constructor.name : 'Object';
			try {
				var keys = Object.keys(v);
				if (keys.length === 0) {
					try { var entries = [...v.entries()]; if (entries.length > 0) return name + ' (' + entries.length + ' entries)'; } catch(e) {}
					return name + ' {}';
				}
				if (keys.length <= 10) return name + ' { ' + keys.join(', ') + ' }';
				return name + ' (' + keys.length + ' keys)';
			} catch(e) { return name; }
		})()`)
		if descErr == nil && desc != nil && !goja.IsUndefined(desc) {
			return desc.String()
		}
	}

	return val.String()
}

func formatJSError(err error, consoleOut string) string {
	var sb strings.Builder

	msg := err.Error()
	if ex, ok := err.(*goja.Exception); ok {
		msg = ex.String()
	}

	msg = sanitizeJSErrorMessage(msg)

	sb.WriteString(msg)

	if consoleOut != "" {
		sb.WriteString("\n\nConsole output before error:\n")
		sb.WriteString(sanitizeJSConsoleOutput(consoleOut))
	}
	return sb.String()
}

func sanitizeJSErrorMessage(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteString(" — ")
		case r == '\t':
			b.WriteString(" ")
		case unicode.IsControl(r):
			b.WriteString(" ")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func sanitizeJSConsoleOutput(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteRune(r)
		case r == '\t':
			b.WriteString("  ")
		case unicode.IsControl(r):
			b.WriteString(" ")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
