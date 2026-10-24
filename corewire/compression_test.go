package corewire

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math/rand"
	"net"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

type memoryCompressionStream struct {
	io.Reader
	bytes.Buffer
	class Class
}

func (s *memoryCompressionStream) Read(p []byte) (int, error) { return s.Reader.Read(p) }
func (s *memoryCompressionStream) Close() error               { return nil }
func (s *memoryCompressionStream) CloseWrite() error          { return nil }
func (s *memoryCompressionStream) Class() Class               { return s.class }

func compressedBytes(t testing.TB, class Class, writes ...[]byte) []byte {
	t.Helper()
	raw := &memoryCompressionStream{class: class}
	s := networkStream(raw, CarrierSSH)
	defer s.Close()
	for _, p := range writes {
		if _, err := s.Write(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	return bytes.Clone(raw.Bytes())
}

func decodeCompression(data []byte, step int) ([]byte, error) {
	raw := &memoryCompressionStream{Reader: &fragmentReader{b: data, step: step}}
	s := networkStream(raw, CarrierSSH)
	defer s.Close()
	return io.ReadAll(s)
}

func TestCompressionModesAndLocalBypass(t *testing.T) {
	for _, tt := range []struct {
		class Class
		size  int
		mode  byte
	}{
		{ClassControl, 4096, 0}, {ClassInput, 4096, 0},
		{ClassInteractive, compressionThreshold - 1, 0},
		{ClassInteractive, compressionThreshold, 1}, {ClassBulk, 4096, 1},
	} {
		want := bytes.Repeat([]byte{'x'}, tt.size)
		wire := compressedBytes(t, tt.class, want)
		if wire[0] != tt.mode {
			t.Fatalf("class %d, size %d mode %d", tt.class, tt.size, wire[0])
		}
		got, err := decodeCompression(wire, 1)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("roundtrip %d/%d: %v", len(got), len(want), err)
		}
	}
	raw := &memoryCompressionStream{}
	if networkStream(raw, CarrierLocal) != raw {
		t.Fatal("local carrier wrapped")
	}
	if wire := compressedBytes(t, ClassInteractive); !bytes.Equal(wire, []byte{0}) {
		t.Fatalf("empty FIN: %x", wire)
	}
}

func TestCompressionStandardFrameAndContextTakeover(t *testing.T) {
	// A random first block cannot compress itself. Repeating it on the SAME
	// stream must use history, not recreate an independent frame per write.
	block := make([]byte, 8192)
	_, _ = rand.New(rand.NewSource(42)).Read(block)
	wire := compressedBytes(t, ClassInteractive, block, block, block)
	var zframe []byte
	var sizes []int
	for p := wire[1:]; len(p) > 0; {
		if len(p) < 8 {
			t.Fatal("short record")
		}
		n := int(binary.BigEndian.Uint32(p[4:8]))
		if n > len(p)-8 {
			t.Fatal("bad record size")
		}
		zframe = append(zframe, p[8:8+n]...)
		sizes = append(sizes, n)
		p = p[8+n:]
	}
	if len(sizes) != 4 || sizes[1] > len(block)/8 || sizes[2] > len(block)/8 {
		t.Fatalf("context not retained: encoded record sizes %v", sizes)
	}
	d, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err := d.DecodeAll(zframe, nil)
	want := bytes.Repeat(block, 3)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("not standard zstd: %v", err)
	}
	if len(wire) > len(want)/2 {
		t.Fatalf("wire ratio %d/%d", len(wire), len(want))
	}
	t.Logf("context record bytes %v; wire %d / raw %d", sizes, len(wire), len(want))
	for step := 1; step <= 127; step += 7 {
		got, err := decodeCompression(wire, step)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("fragment %d: %d bytes %v", step, len(got), err)
		}
	}
}

func TestCompressionRandomWritesAndMessages(t *testing.T) {
	rng := rand.New(rand.NewSource(71))
	var writes [][]byte
	var want []byte
	for range 80 {
		p := make([]byte, 256+rng.Intn(3*compressionChunk))
		_, _ = rng.Read(p)
		writes = append(writes, p)
		want = append(want, p...)
	}
	wire := compressedBytes(t, ClassBulk, writes...)
	got, err := decodeCompression(wire, 1+rng.Intn(123))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("random writes %d/%d: %v", len(got), len(want), err)
	}
	// Exercise the actual message entry point, rather than only direct adapter
	// writes. Its length prefix must not accidentally select identity first.
	raw := &memoryCompressionStream{class: ClassInteractive}
	s := networkStream(raw, CarrierTCP)
	defer s.Close()
	body := bytes.Repeat([]byte("topic update"), 1000)
	if err := WriteMessage(s, body); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if raw.Bytes()[0] != 1 || raw.Len() > len(body)/4 {
		t.Fatal("messages bypass compression")
	}
	decoded, err := decodeCompression(raw.Bytes(), 11)
	if err != nil {
		t.Fatal(err)
	}
	got, err = NewReader(bytes.NewReader(decoded), 0).Next()
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("message changed: %v", err)
	}
}

func networkMuxPair(t testing.TB, carrier Carrier) (*Mux, *Mux) {
	t.Helper()
	a, b := net.Pipe()
	c, s := NewMux(a, carrier, true), NewMux(b, carrier, false)
	t.Cleanup(func() { _ = c.Close(); _ = s.Close() })
	return c, s
}

func TestCompressionActualCarriersFlushAndHalfClose(t *testing.T) {
	for _, carrier := range []Carrier{CarrierLocal, CarrierSSH, CarrierTCP, CarrierQUIC} {
		t.Run(string(carrier), func(t *testing.T) {
			var client, server Conn
			if carrier == CarrierQUIC {
				client, server = quicPair(t)
			} else {
				client, server = networkMuxPair(t, carrier)
			}
			s, err := client.OpenStream(context.Background(), ClassInteractive)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			peer := accept(t, server)
			defer peer.Close()
			want := bytes.Repeat([]byte("terminal frame and transcript append"), 3000)
			done := make(chan error, 1)
			go func() { _, err := s.Write(want); done <- err }()
			got := make([]byte, len(want))
			read := make(chan error, 1)
			go func() { _, err := io.ReadFull(peer, got); read <- err }()
			select {
			case err := <-read:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Flush waited for FIN")
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatal("stream content changed")
			}
			if err := s.CloseWrite(); err != nil {
				t.Fatal(err)
			}
			if _, err := peer.Read(got[:1]); err != io.EOF {
				t.Fatalf("half-close: %v", err)
			}
			if _, err := peer.Write([]byte("reply")); err != nil {
				t.Fatal(err)
			}
			if err := peer.CloseWrite(); err != nil {
				t.Fatal(err)
			}
			got, err = io.ReadAll(s)
			if err != nil || string(got) != "reply" {
				t.Fatalf("reply %q: %v", got, err)
			}
			if _, err := s.Write([]byte("late")); !errors.Is(err, ErrClosed) {
				t.Fatalf("late write: %v", err)
			}
		})
	}
}

func TestCompressionRejectsMalformedTruncatedAndBomb(t *testing.T) {
	valid := compressedBytes(t, ClassBulk, bytes.Repeat([]byte("abc"), 1000))
	cases := map[string][]byte{"mode": {2}, "missing-mode": {}, "trailing": append(bytes.Clone(valid), 'x')}
	for _, cut := range []int{1, 3, 9, len(valid) - 1, len(valid) - 8} {
		cases["truncated-"+strconv.Itoa(cut)] = bytes.Clone(valid[:cut])
	}
	for _, field := range []int{1, 5} {
		bad := bytes.Clone(valid)
		binary.BigEndian.PutUint32(bad[field:field+4], ^uint32(0))
		cases["length-"+strconv.Itoa(field)] = bad
	}
	bomb := bytes.Clone(valid)
	binary.BigEndian.PutUint32(bomb[1:5], 1)
	cases["forged-raw"] = bomb
	badCRC := bytes.Clone(valid)
	badCRC[len(badCRC)-1] ^= 1
	cases["checksum"] = badCRC
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeCompression(data, 17); !errors.Is(err, ErrProtocol) {
				t.Fatalf("malformed accepted: %v", err)
			}
		})
	}
	// A standard but forbidden large-window frame must fail before its decoder
	// allocates that window, regardless of the envelope's plausible lengths.
	var encoded bytes.Buffer
	e, _ := zstd.NewWriter(&encoded, zstd.WithEncoderConcurrency(1), zstd.WithWindowSize(1<<20))
	_, _ = e.Write(bytes.Repeat([]byte("x"), 3000))
	_ = e.Flush()
	_ = e.Close()
	large := []byte{1}
	large = binary.BigEndian.AppendUint32(large, 3000)
	large = binary.BigEndian.AppendUint32(large, uint32(encoded.Len()))
	large = append(large, encoded.Bytes()...)
	if _, err := decodeCompression(large, 123); !errors.Is(err, ErrProtocol) {
		t.Fatalf("large window accepted: %v", err)
	}
}

func TestCompressionCloseInterruptsBlockedDirections(t *testing.T) {
	for _, peerReset := range []bool{false, true} {
		client, server := networkMuxPair(t, CarrierSSH)
		s, _ := client.OpenStream(context.Background(), ClassBulk)
		peer := accept(t, server)
		data := make([]byte, 2*streamWindow)
		_, _ = rand.New(rand.NewSource(19)).Read(data)
		wrote, read, ended := make(chan error, 1), make(chan error, 1), make(chan error, 1)
		go func() { _, err := s.Write(data); wrote <- err }()
		go func() { _, err := s.Read(make([]byte, 1)); read <- err }()
		ms := s.(*compressedStream).Stream.(*muxStream)
		waitFor(t, func() bool { client.mu.Lock(); defer client.mu.Unlock(); return ms.credit == 0 })
		go func() { ended <- s.CloseWrite() }()
		want := ErrClosed
		if peerReset {
			_ = peer.Close()
			want = ErrReset
		} else {
			_ = s.Close()
		}
		for _, ch := range []chan error{wrote, read, ended} {
			select {
			case err := <-ch:
				if !errors.Is(err, want) {
					t.Fatalf("termination %v, want %v", err, want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("close failed to interrupt codec")
			}
		}
		_ = s.Close()
	}
}

func FuzzCompressedStream(f *testing.F) {
	f.Add(compressedBytes(f, ClassInteractive, bytes.Repeat([]byte("seed"), 100)), uint8(1))
	f.Add([]byte{1, 255, 255, 255, 255, 255, 255, 255, 255}, uint8(13))
	f.Add([]byte{0, 'x'}, uint8(7))
	f.Fuzz(func(t *testing.T, data []byte, fragment uint8) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		raw := &memoryCompressionStream{Reader: &fragmentReader{b: data, step: 1 + int(fragment)}}
		s := networkStream(raw, CarrierTCP)
		defer s.Close()
		buf := make([]byte, 4096)
		for range 4096 {
			if _, err := s.Read(buf); err != nil {
				return
			}
		}
		t.Fatal("unbounded output from bounded compressed input")
	})
}

func BenchmarkCompressedMuxInputUnderBulk(b *testing.B) {
	for _, carrier := range []Carrier{CarrierLocal, CarrierSSH} {
		b.Run(string(carrier), func(b *testing.B) {
			client, server := networkMuxPair(b, carrier)
			bulk, _ := client.OpenStream(context.Background(), ClassBulk)
			bulkPeer := accept(b, server)
			var wg sync.WaitGroup
			wg.Add(3)
			go func() { defer wg.Done(); _, _ = io.Copy(io.Discard, bulkPeer) }()
			go func() {
				defer wg.Done()
				data := make([]byte, 1<<20)
				_, _ = rand.New(rand.NewSource(1)).Read(data)
				for {
					if _, err := bulk.Write(data); err != nil {
						return
					}
				}
			}()
			input, _ := client.OpenStream(context.Background(), ClassInput)
			peer := accept(b, server)
			go func() { defer wg.Done(); _, _ = io.Copy(peer, peer) }()
			var key [1]byte
			msg := []byte{'q'}
			var samples []time.Duration
			b.ReportAllocs()
			for b.Loop() {
				start := time.Now()
				if _, err := input.Write(msg); err != nil {
					b.Fatal(err)
				}
				if _, err := io.ReadFull(input, key[:]); err != nil {
					b.Fatal(err)
				}
				samples = append(samples, time.Since(start))
			}
			sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
			if len(samples) > 0 {
				b.ReportMetric(float64(samples[(len(samples)-1)*99/100].Nanoseconds()), "p99-ns")
			}
			_ = bulk.Close()
			_ = bulkPeer.Close()
			_ = input.Close()
			_ = peer.Close()
			wg.Wait()
		})
	}
}
