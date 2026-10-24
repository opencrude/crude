package quic

import (
	"slices"
	"sync"

	"github.com/quic-go/quic-go/internal/ackhandler"
	"github.com/quic-go/quic-go/internal/monotime"
	"github.com/quic-go/quic-go/internal/protocol"
	"github.com/quic-go/quic-go/internal/utils/ringbuffer"
	"github.com/quic-go/quic-go/internal/wire"
	"github.com/quic-go/quic-go/quicvarint"
)

const (
	maxPathResponses = 256
	maxControlFrames = 16 << 10
)

// This is the largest possible size of a stream-related control frame
// (which is the RESET_STREAM frame).
const maxStreamControlFrameSize = 25

type streamFrameGetter interface {
	popStreamFrame(protocol.ByteCount, protocol.Version) (ackhandler.StreamFrame, *wire.StreamDataBlockedFrame, bool)
}

type streamControlFrameGetter interface {
	getControlFrame(monotime.Time) (_ ackhandler.Frame, ok, hasMore bool)
}

type framer struct {
	mutex sync.Mutex

	activeStreams            map[protocol.StreamID]streamFrameGetter
	streamQueues             [4]ringbuffer.RingBuffer[protocol.StreamID]
	streamPriorities         map[protocol.StreamID]uint8
	priorityBudget           [4]int
	streamsWithControlFrames map[protocol.StreamID]streamControlFrameGetter

	controlFrameMutex          sync.Mutex
	controlFrames              []wire.Frame
	pathResponses              []*wire.PathResponseFrame
	connFlowController         *connectionFlowController
	queuedTooManyControlFrames bool
}

func newFramer(connFlowController *connectionFlowController) *framer {
	return &framer{
		activeStreams:            make(map[protocol.StreamID]streamFrameGetter),
		streamPriorities:         make(map[protocol.StreamID]uint8),
		streamsWithControlFrames: make(map[protocol.StreamID]streamControlFrameGetter),
		connFlowController:       connFlowController,
	}
}

func (f *framer) HasData() bool {
	f.mutex.Lock()
	hasData := len(f.activeStreams) > 0
	f.mutex.Unlock()
	if hasData {
		return true
	}
	f.controlFrameMutex.Lock()
	defer f.controlFrameMutex.Unlock()
	return len(f.streamsWithControlFrames) > 0 || len(f.controlFrames) > 0 || len(f.pathResponses) > 0
}

func (f *framer) QueueControlFrame(frame wire.Frame) {
	f.controlFrameMutex.Lock()
	defer f.controlFrameMutex.Unlock()

	if pr, ok := frame.(*wire.PathResponseFrame); ok {
		// Only queue up to maxPathResponses PATH_RESPONSE frames.
		// This limit should be high enough to never be hit in practice,
		// unless the peer is doing something malicious.
		if len(f.pathResponses) >= maxPathResponses {
			return
		}
		f.pathResponses = append(f.pathResponses, pr)
		return
	}
	// This is a hack.
	if len(f.controlFrames) >= maxControlFrames {
		f.queuedTooManyControlFrames = true
		return
	}
	f.controlFrames = append(f.controlFrames, frame)
}

func (f *framer) Append(
	frames []ackhandler.Frame,
	streamFrames []ackhandler.StreamFrame,
	maxLen protocol.ByteCount,
	now monotime.Time,
	v protocol.Version,
) ([]ackhandler.Frame, []ackhandler.StreamFrame, protocol.ByteCount) {
	f.controlFrameMutex.Lock()
	frames, controlFrameLen := f.appendControlFrames(frames, maxLen, now, v)
	maxLen -= controlFrameLen

	var lastFrame ackhandler.StreamFrame
	var streamFrameLen protocol.ByteCount
	f.mutex.Lock()
	// pop STREAM frames, until less than 128 bytes are left in the packet
	numActiveStreams := len(f.activeStreams)
	for range numActiveStreams {
		if protocol.MinStreamFrameSize > maxLen {
			break
		}
		sf, blocked := f.getNextStreamFrame(maxLen, v)
		if sf.Frame != nil {
			streamFrames = append(streamFrames, sf)
			maxLen -= sf.Frame.Length(v)
			lastFrame = sf
			streamFrameLen += sf.Frame.Length(v)
		}
		// If the stream just became blocked on stream flow control, attempt to pack the
		// STREAM_DATA_BLOCKED into the same packet.
		if blocked != nil {
			l := blocked.Length(v)
			// In case it doesn't fit, queue it for the next packet.
			if maxLen < l {
				f.controlFrames = append(f.controlFrames, blocked)
				break
			}
			frames = append(frames, ackhandler.Frame{Frame: blocked})
			maxLen -= l
			controlFrameLen += l
		}
	}

	// The only way to become blocked on connection-level flow control is by sending STREAM frames.
	if isBlocked, offset := f.connFlowController.IsNewlyBlocked(); isBlocked {
		blocked := &wire.DataBlockedFrame{MaximumData: offset}
		l := blocked.Length(v)
		// In case it doesn't fit, queue it for the next packet.
		if maxLen >= l {
			frames = append(frames, ackhandler.Frame{Frame: blocked})
			controlFrameLen += l
		} else {
			f.controlFrames = append(f.controlFrames, blocked)
		}
	}

	f.mutex.Unlock()
	f.controlFrameMutex.Unlock()

	if lastFrame.Frame != nil {
		// account for the smaller size of the last STREAM frame
		streamFrameLen -= lastFrame.Frame.Length(v)
		lastFrame.Frame.DataLenPresent = false
		streamFrameLen += lastFrame.Frame.Length(v)
	}

	return frames, streamFrames, controlFrameLen + streamFrameLen
}

func (f *framer) appendControlFrames(
	frames []ackhandler.Frame,
	maxLen protocol.ByteCount,
	now monotime.Time,
	v protocol.Version,
) ([]ackhandler.Frame, protocol.ByteCount) {
	var length protocol.ByteCount
	// add a PATH_RESPONSE first, but only pack a single PATH_RESPONSE per packet
	if len(f.pathResponses) > 0 {
		frame := f.pathResponses[0]
		frameLen := frame.Length(v)
		if frameLen <= maxLen {
			frames = append(frames, ackhandler.Frame{Frame: frame})
			length += frameLen
			f.pathResponses = f.pathResponses[1:]
		}
	}

	// add stream-related control frames
	for id, str := range f.streamsWithControlFrames {
	start:
		remainingLen := maxLen - length
		if remainingLen <= maxStreamControlFrameSize {
			break
		}
		fr, ok, hasMore := str.getControlFrame(now)
		if !hasMore {
			delete(f.streamsWithControlFrames, id)
		}
		if !ok {
			continue
		}
		frames = append(frames, fr)
		length += fr.Frame.Length(v)
		if hasMore {
			// It is rare that a stream has more than one control frame to queue.
			// We don't want to spawn another loop for just to cover that case.
			goto start
		}
	}

	for len(f.controlFrames) > 0 {
		frame := f.controlFrames[len(f.controlFrames)-1]
		frameLen := frame.Length(v)
		if length+frameLen > maxLen {
			break
		}
		frames = append(frames, ackhandler.Frame{Frame: frame})
		length += frameLen
		f.controlFrames = f.controlFrames[:len(f.controlFrames)-1]
	}

	return frames, length
}

// QueuedTooManyControlFrames says if the control frame queue exceeded its maximum queue length.
// This is a hack.
// It is easier to implement than propagating an error return value in QueueControlFrame.
// The correct solution would be to queue frames with their respective structs.
// See https://github.com/quic-go/quic-go/issues/4271 for the queueing of stream-related control frames.
func (f *framer) QueuedTooManyControlFrames() bool {
	return f.queuedTooManyControlFrames
}

func (f *framer) AddActiveStream(id protocol.StreamID, str streamFrameGetter) {
	f.mutex.Lock()
	if _, ok := f.activeStreams[id]; !ok {
		priority := streamPriority(str)
		f.streamQueues[priority].PushBack(id)
		f.streamPriorities[id] = priority
		f.activeStreams[id] = str
	}
	f.mutex.Unlock()
}

func streamPriority(str streamFrameGetter) uint8 {
	if p, ok := str.(interface{ streamPriority() uint8 }); ok {
		return p.streamPriority()
	}
	return 2
}

// Priority changes are rare and may occur while a stream is already active.
// Remove the old queue entry instead of retaining unbounded tombstones.
func (f *framer) removeQueuedStream(id protocol.StreamID) {
	priority, ok := f.streamPriorities[id]
	if !ok {
		return
	}
	q := &f.streamQueues[priority]
	for range q.Len() {
		candidate := q.PopFront()
		if candidate != id {
			q.PushBack(candidate)
		}
	}
}

func (f *framer) SetStreamPriority(id protocol.StreamID, priority uint8) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	// Read the authoritative atomic value under the scheduler lock: concurrent
	// setters may deliver their notifications in the opposite order.
	if str, ok := f.activeStreams[id]; ok {
		priority = streamPriority(str)
	}
	old, ok := f.streamPriorities[id]
	if !ok || old == priority {
		return
	}
	f.removeQueuedStream(id)
	f.streamPriorities[id] = priority
	f.streamQueues[priority].PushBack(id)
}

func (f *framer) nextPriorityStream() protocol.StreamID {
	for range 2 {
		for p := range f.streamQueues {
			if f.priorityBudget[p] > 0 && !f.streamQueues[p].Empty() {
				f.priorityBudget[p]--
				return f.streamQueues[p].PopFront()
			}
		}
		f.priorityBudget = [4]int{8, 4, 2, 1}
	}
	panic("quic: priority scheduling an empty framer")
}

func (f *framer) AddStreamWithControlFrames(id protocol.StreamID, str streamControlFrameGetter) {
	f.controlFrameMutex.Lock()
	if _, ok := f.streamsWithControlFrames[id]; !ok {
		f.streamsWithControlFrames[id] = str
	}
	f.controlFrameMutex.Unlock()
}

// RemoveActiveStream is called when a stream completes.
func (f *framer) RemoveActiveStream(id protocol.StreamID) {
	f.mutex.Lock()
	f.removeQueuedStream(id)
	delete(f.activeStreams, id)
	delete(f.streamPriorities, id)
	f.mutex.Unlock()
}

func (f *framer) getNextStreamFrame(maxLen protocol.ByteCount, v protocol.Version) (ackhandler.StreamFrame, *wire.StreamDataBlockedFrame) {
	id := f.nextPriorityStream()
	// This should never return an error. Better check it anyway.
	// The stream will only be in the streamQueue, if it enqueued itself there.
	str, ok := f.activeStreams[id]
	// The stream might have been removed after being enqueued.
	if !ok {
		return ackhandler.StreamFrame{}, nil
	}
	// For the last STREAM frame, we'll remove the DataLen field later.
	// Therefore, we can pretend to have more bytes available when popping
	// the STREAM frame (which will always have the DataLen set).
	maxLen += protocol.ByteCount(quicvarint.Len(uint64(maxLen)))
	frame, blocked, hasMoreData := str.popStreamFrame(maxLen, v)
	if hasMoreData { // put the stream back in the queue (at the end)
		f.streamQueues[f.streamPriorities[id]].PushBack(id)
	} else { // no more data to send. Stream is not active
		delete(f.activeStreams, id)
		delete(f.streamPriorities, id)
	}
	// Note that the frame.Frame can be nil:
	// * if the stream was canceled after it said it had data
	// * the remaining size doesn't allow us to add another STREAM frame
	return frame, blocked
}

func (f *framer) Handle0RTTRejection() {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.controlFrameMutex.Lock()
	defer f.controlFrameMutex.Unlock()

	for i := range f.streamQueues {
		f.streamQueues[i].Clear()
	}
	clear(f.streamPriorities)
	f.priorityBudget = [4]int{}
	for id := range f.activeStreams {
		delete(f.activeStreams, id)
	}
	clear(f.streamsWithControlFrames)
	var j int
	for i, frame := range f.controlFrames {
		switch frame.(type) {
		case *wire.MaxDataFrame, *wire.MaxStreamDataFrame, *wire.MaxStreamsFrame,
			*wire.DataBlockedFrame, *wire.StreamDataBlockedFrame, *wire.StreamsBlockedFrame:
			continue
		default:
			f.controlFrames[j] = f.controlFrames[i]
			j++
		}
	}
	f.controlFrames = slices.Delete(f.controlFrames, j, len(f.controlFrames))
}
