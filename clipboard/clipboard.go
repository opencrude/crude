package clipboard

import (
	"errors"
	"sync"
)

var (
	errUnavailable = errors.New("clipboard unavailable")
	errUnsupported = errors.New("unsupported format")
)

type Format int

const (
	FmtText Format = iota
	FmtImage
)

var (
	lock      sync.Mutex
	initOnce  sync.Once
	initError error
)

func Read(t Format) []byte {
	lock.Lock()
	defer lock.Unlock()

	buf, err := read(t)
	if err != nil {
		return nil
	}
	return buf
}

func WriteText(text string) error {
	return Write(FmtText, []byte(text))
}

func Write(t Format, buf []byte) error {
	lock.Lock()
	defer lock.Unlock()

	_, err := write(t, buf)
	return err
}

func GetError() error {
	return initError
}
