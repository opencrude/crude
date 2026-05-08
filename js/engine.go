package js

import (
	"fmt"
	"strings"
	"time"

	"github.com/dop251/goja"
)

const (
	maxExecTime = 10 * time.Second
	maxCodeLen  = 50000
)

// Run executes JavaScript code in a sandboxed goja VM and returns the result.
// The last expression value is returned as a string.
func Run(code string) (string, error) {
	if code == "" {
		return "", fmt.Errorf("js: empty code")
	}
	if len(code) > maxCodeLen {
		return "", fmt.Errorf("js: code too long (%d chars, max %d)", len(code), maxCodeLen)
	}

	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))

	// Capture console.log output.
	var consoleOut strings.Builder
	consoleObj := vm.NewObject()
	_ = consoleObj.Set("log", func(args ...goja.Value) {
		parts := make([]string, 0, len(args))
		for _, a := range args {
			parts = append(parts, a.String())
		}
		consoleOut.WriteString(strings.Join(parts, " "))
		consoleOut.WriteByte('\n')
	})
	_ = consoleObj.Set("error", func(args ...goja.Value) {
		parts := make([]string, 0, len(args))
		for _, a := range args {
			parts = append(parts, a.String())
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
		return "", fmt.Errorf("js: execution timed out (%s)", maxExecTime)
	}

	if err != nil {
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
	if out == "" {
		return "(no output)", nil
	}
	return out, nil
}

func formatValue(vm *goja.Runtime, val goja.Value) string {
	if val == nil || goja.IsUndefined(val) || goja.IsNull(val) {
		return "undefined"
	}

	// Try JSON.stringify for objects.
	if obj, ok := val.(*goja.Object); ok {
		jsonStr, err := vm.RunString("JSON.stringify(" + obj.String() + ", null, 2)")
		if err == nil && !goja.IsUndefined(jsonStr) {
			s := jsonStr.String()
			if s != "undefined" && s != "" {
				return s
			}
		}
	}
	return val.String()
}

func formatJSError(err error, consoleOut string) string {
	var sb strings.Builder
	sb.WriteString("JavaScript error: ")
	sb.WriteString(err.Error())
	if consoleOut != "" {
		sb.WriteString("\n\nConsole output before error:\n")
		sb.WriteString(consoleOut)
	}
	return sb.String()
}
