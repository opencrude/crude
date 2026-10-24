//go:build !cgo

package terminalvt

func (e *Engine) StreamRows(request RowRequest) (Projection, error) { return Projection{}, ErrNoCGO }
