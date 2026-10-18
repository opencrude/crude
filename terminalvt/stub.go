//go:build !cgo

package terminalvt

// Engine preserves the neutral API but fails closed when CGO is unavailable.
type Engine struct{}

func New(Options) (*Engine, error)                    { return nil, ErrNoCGO }
func (e *Engine) Close()                              {}
func (e *Engine) Feed([]byte) Effects                 { return Effects{} }
func (e *Engine) Resize(int, int) error               { return ErrNoCGO }
func (e *Engine) Rows(RowRequest) (Projection, error) { return Projection{}, ErrNoCGO }
func (e *Engine) EncodeKey(Key) []byte                { return nil }
func (e *Engine) EncodeMouse(Mouse) []byte            { return nil }
func (e *Engine) EncodeFocus(bool) []byte             { return nil }
func (e *Engine) EncodePaste([]byte) []byte           { return nil }
