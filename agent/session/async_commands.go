package session

import "crude/workspace/fileio"

func (s *Session) KillAsyncCommand(ref fileio.AsyncCmdRef) error {
	if s == nil || s.asyncMgr == nil {
		return fileio.ErrAsyncCommandNotFound
	}
	return s.asyncMgr.KillRef(ref)
}

// ObserveAsyncOutput observes a command without advancing the model's cursor.
func (s *Session) ObserveAsyncOutput(ref fileio.AsyncCmdRef, sinceByte, maxBytes int) ([]byte, int, fileio.AsyncCmdInfo, <-chan struct{}, error) {
	if s == nil || s.asyncMgr == nil {
		return nil, 0, fileio.AsyncCmdInfo{}, nil, fileio.ErrAsyncCommandNotFound
	}
	return s.asyncMgr.ObserveOutput(ref, sinceByte, maxBytes)
}
