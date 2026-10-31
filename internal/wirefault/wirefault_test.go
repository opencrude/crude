package wirefault

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"

	"crude/coreprotocol"
	"crude/corewire"
)

func replyBytes(t *testing.T, reply coreprotocol.Reply) []byte {
	t.Helper()
	payload, err := coreprotocol.Marshal(&reply)
	if err != nil {
		t.Fatal(err)
	}
	var framed bytes.Buffer
	if err := corewire.WriteMessage(&framed, payload); err != nil {
		t.Fatal(err)
	}
	return framed.Bytes()
}

func muxFrame(id uint64, kind byte, payload []byte) []byte {
	frame := binary.AppendUvarint(nil, id)
	frame = append(frame, kind)
	frame = binary.AppendUvarint(frame, uint64(len(payload)))
	return append(frame, payload...)
}

func TestDropReplySignalRequiresCompleteFinalNotAdmission(t *testing.T) {
	server, peer := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = peer.Close() })
	drop := NewDropReplies(server, "mutation")
	drop.methods[7] = "mutation"
	write := func(data []byte) {
		t.Helper()
		if n, err := drop.Write(data); err != nil || n != len(data) {
			t.Fatal("fault injector failed to accept target frame", n, err)
		}
	}
	notFinal := func() {
		t.Helper()
		select {
		case <-drop.Dropped:
			t.Fatal("admission/progress/incomplete final became completed-effect evidence")
		default:
		}
	}
	note := muxFrame(7, frameData, replyBytes(t, coreprotocol.Reply{Admitted: true}))
	// Partial carrier writes are distinct from partial application messages.
	write(note[:2])
	notFinal()
	write(note[2:])
	notFinal()
	write(muxFrame(7, frameData, replyBytes(t, coreprotocol.Reply{Progress: "still running"})))
	notFinal()
	final := replyBytes(t, coreprotocol.Reply{Final: true})
	write(muxFrame(7, frameData, final[:len(final)-1]))
	notFinal()
	write(muxFrame(7, frameData, final[len(final)-1:]))
	select {
	case <-drop.Dropped:
	default:
		t.Fatal("lost final reply did not signal the original effect boundary")
	}
	write(muxFrame(7, frameFin, nil))
}

func TestDropRepliesForwardsOtherMethodsUnchanged(t *testing.T) {
	server, peer := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = peer.Close() })
	drop := NewDropReplies(server, "mutation")
	drop.methods[9] = "observation"
	data := muxFrame(9, frameData, replyBytes(t, coreprotocol.Reply{Final: true}))
	done := make(chan error, 1)
	go func() { _, err := drop.Write(data); done <- err }()
	got := make([]byte, len(data))
	if _, err := io.ReadFull(peer, got); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil || !bytes.Equal(got, data) {
		t.Fatal("unrelated receipt was changed or lost", err)
	}
	select {
	case <-drop.Dropped:
		t.Fatal("another method's final was attributed to the held mutation")
	default:
	}
}
