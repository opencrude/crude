package quic

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/quic-go/quic-go/internal/ackhandler"
	"github.com/quic-go/quic-go/internal/monotime"
	"github.com/quic-go/quic-go/internal/protocol"
	"github.com/quic-go/quic-go/internal/wire"
)

type prioritySource struct {
	priority atomic.Uint32
	id       protocol.StreamID
	frame    wire.StreamFrame
}

func (s *prioritySource) streamPriority() uint8 { return uint8(s.priority.Load()) }
func (s *prioritySource) popStreamFrame(max protocol.ByteCount, _ protocol.Version) (ackhandler.StreamFrame, *wire.StreamDataBlockedFrame, bool) {
	s.frame = wire.StreamFrame{StreamID: s.id, DataLenPresent: true, Data: []byte{1}}
	return ackhandler.StreamFrame{Frame: &s.frame}, nil, true
}
func priorityFramer() *framer { return newFramer(newConnectionFlowController(0, 0, nil, nil, nil)) }
func addPriority(f *framer, id protocol.StreamID, p uint8) *prioritySource {
	s := &prioritySource{id: id}
	s.priority.Store(uint32(p))
	f.AddActiveStream(id, s)
	return s
}

func TestPriorityWeightedPacketFrames(t *testing.T) {
	f := priorityFramer()
	for p := uint8(0); p < 4; p++ {
		addPriority(f, protocol.StreamID(p), p)
	}
	counts := [4]int{}
	// The actual packet append path, not only queue selection, must serve all
	// classes. A packet budget permitting one frame fixes the service unit.
	for range 150 {
		_, frames, _ := f.Append(nil, nil, protocol.MinStreamFrameSize, monotime.Now(), protocol.Version1)
		if len(frames) != 1 {
			t.Fatalf("packed %d frames", len(frames))
		}
		counts[frames[0].Frame.StreamID]++
	}
	if counts != [4]int{80, 40, 20, 10} {
		t.Fatalf("class service = %v", counts)
	}
}

func TestPriorityReclassifiesPendingAndRejectsStaleNotification(t *testing.T) {
	f := priorityFramer()
	addPriority(f, 1, 3)
	s := addPriority(f, 2, 3)
	s.priority.Store(1)
	f.SetStreamPriority(2, 3) // stale notification must reread current class 1
	if f.streamQueues[3].Len() != 1 || f.streamQueues[1].Len() != 1 {
		t.Fatal("queued priority not updated")
	}
	if got := f.nextPriorityStream(); got != 2 {
		t.Fatalf("urgent stream %d waits behind bulk", got)
	}
	f.streamQueues[1].PushBack(2)
	for range 1000 {
		s.priority.Store(3)
		f.SetStreamPriority(2, 3)
		s.priority.Store(1)
		f.SetStreamPriority(2, 1)
	}
	if f.streamQueues[3].Len()+f.streamQueues[1].Len() != 2 {
		t.Fatal("priority updates grew queued entries")
	}
	f.RemoveActiveStream(2)
	if f.streamQueues[1].Len() != 0 || len(f.streamPriorities) != 1 {
		t.Fatal("completed stream retains scheduling data")
	}
	f.Handle0RTTRejection()
	for _, q := range f.streamQueues {
		if !q.Empty() {
			t.Fatal("0-RTT reset retained queue")
		}
	}
	if len(f.streamPriorities) != 0 {
		t.Fatal("0-RTT reset retained metadata")
	}
}

type priorityNotifier struct {
	streamSender
	f *framer
}

func (s priorityNotifier) onStreamPriority(id protocol.StreamID, p uint8) {
	s.f.SetStreamPriority(id, p)
}

func TestPriorityConcurrentAPIAndPacketizer(t *testing.T) {
	f := priorityFramer()
	s := newSendStream(context.Background(), 1, priorityNotifier{f: f}, nil, false)
	if s.streamPriority() != 2 {
		t.Fatal("default priority is not interactive")
	}
	s.SetPriority(255)
	if s.streamPriority() != 2 {
		t.Fatal("invalid priority accepted")
	}
	// Register a separate getter sharing the atomic priority, so the test can
	// exercise scheduling without depending on QUIC flow-controller setup.
	getter := &sendPrioritySource{send: s, prioritySource: prioritySource{id: 1}}
	f.AddActiveStream(1, getter)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 1000 {
				s.SetPriority(uint8(i % 4))
			}
		}()
	}
	for range 1000 {
		f.Append(nil, nil, protocol.MinStreamFrameSize, monotime.Now(), protocol.Version1)
	}
	wg.Wait()
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if f.streamPriorities[1] != s.streamPriority() {
		t.Fatal("last update not reflected in pending data")
	}
}

type sendPrioritySource struct {
	send *SendStream
	prioritySource
}

func (s *sendPrioritySource) streamPriority() uint8 { return s.send.streamPriority() }

func BenchmarkPriorityFramer(b *testing.B) {
	f := priorityFramer()
	for p := uint8(0); p < 4; p++ {
		addPriority(f, protocol.StreamID(p), p)
	}
	b.ReportAllocs()
	for b.Loop() {
		id := f.nextPriorityStream()
		f.streamQueues[f.streamPriorities[id]].PushBack(id)
	}
}

func FuzzPriorityScheduling(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 7, 11, 15, 31, 63})
	f.Fuzz(func(t *testing.T, actions []byte) {
		fr := priorityFramer()
		streams := [16]*prioritySource{}
		for i := range streams {
			streams[i] = &prioritySource{id: protocol.StreamID(i)}
		}
		for _, action := range actions {
			id := protocol.StreamID(action & 15)
			s := streams[id]
			switch action >> 6 {
			case 0:
				s.priority.Store(uint32(action >> 4))
				fr.AddActiveStream(id, s)
			case 1:
				s.priority.Store(uint32((action >> 4) & 3))
				fr.SetStreamPriority(id, uint8((action>>4)&3))
			case 2:
				fr.RemoveActiveStream(id)
			case 3:
				if len(fr.activeStreams) > 0 {
					fr.Append(nil, nil, protocol.MinStreamFrameSize, monotime.Now(), protocol.Version1)
				}
			}
			seen := map[protocol.StreamID]bool{}
			for p := range fr.streamQueues {
				q := &fr.streamQueues[p]
				for range q.Len() {
					candidate := q.PopFront()
					q.PushBack(candidate)
					if seen[candidate] || fr.activeStreams[candidate] == nil || fr.streamPriorities[candidate] != uint8(p) {
						t.Fatal("invalid scheduling state")
					}
					seen[candidate] = true
				}
			}
			if len(seen) != len(fr.activeStreams) {
				t.Fatal("active stream lost from scheduling")
			}
		}
	})
}
