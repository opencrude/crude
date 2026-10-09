package corewire

import (
	"context"
	"strings"
	"testing"
)

func TestSSHArgumentsAndValidation(t *testing.T) {
	args, err := sshArgs(SSHOptions{Host: "example.org", User: "alice", Port: 2222, IdentityFile: "/tmp/a key"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "|")
	for _, required := range []string{"StrictHostKeyChecking=yes", "ClearAllForwardings=yes", "ForwardAgent=no", "PermitLocalCommand=no", "--|example.org|crude --core-stdio", "-i|/tmp/a key", "-p|2222"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("missing %s in %s", required, joined)
		}
	}
	for _, host := range []string{"-oProxyCommand=evil", "a;evil", "a b", "user@host", "a\n"} {
		if _, err := DialSSH(context.Background(), SSHOptions{Host: host}); err == nil {
			t.Fatalf("accepted %q", host)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DialSSH(ctx, SSHOptions{Host: "example.org"}); err == nil {
		t.Fatal("started cancelled SSH")
	}
}
