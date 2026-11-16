//go:build !cgo

package terminalvt

func (e *Engine) Snapshot() ([]byte, error) { return nil, ErrNoCGO }
