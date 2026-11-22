package hostenv

// SSHSession reports an SSH login. OpenSSH sets these variables on every
// platform; a notification from such a process would reach the screen of
// the computer it runs on, or nobody.
func SSHSession(getenv func(string) string) bool {
	return getenv("SSH_CONNECTION") != "" || getenv("SSH_CLIENT") != "" || getenv("SSH_TTY") != ""
}
