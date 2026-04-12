package logger

import (
	"os"
	"sync"
)

type FileWriter struct {
	mu   sync.Mutex
	file *os.File
}

func NewFileWriter(path string, perm os.FileMode) (*FileWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, perm)
	if err != nil {
		return nil, err
	}
	return &FileWriter{file: f}, nil
}

func (fw *FileWriter) Write(p []byte) (int, error) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return fw.file.Write(p)
}

func (fw *FileWriter) Close() error {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return fw.file.Close()
}

func (fw *FileWriter) Path() string {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return fw.file.Name()
}
