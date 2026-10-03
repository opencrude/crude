package session

import "crude/fileio"

func (s *Session) KillAsyncCommand(ref fileio.AsyncCmdRef) error {
	if s == nil || s.asyncMgr == nil {
		return fileio.ErrAsyncCommandNotFound
	}
	return s.asyncMgr.KillRef(ref)
}
