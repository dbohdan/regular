package main

import (
	"bytes"
	"sync"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestRequestRoundTrip(t *testing.T) {
	in := Request{Verb: verbRun, Job: "backup", Force: true}

	encoded, err := msgpack.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out Request
	if err := msgpack.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out != in {
		t.Errorf("round trip mismatch: got %+v, want %+v", out, in)
	}
}

func TestFrameRoundTripVariants(t *testing.T) {
	cases := []Frame{
		{Type: frameStdout, Data: []byte("hello\nworld\x00\xff")},
		{Type: frameStderr, Data: []byte{}},
		{Type: frameLog, Msg: "Started"},
		{Type: frameExit, Code: 7, Error: "boom"},
	}

	for _, in := range cases {
		t.Run(in.Type, func(t *testing.T) {
			encoded, err := msgpack.Marshal(in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var out Frame
			if err := msgpack.Unmarshal(encoded, &out); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			if out.Type != in.Type || !bytes.Equal(out.Data, in.Data) ||
				out.Msg != in.Msg || out.Code != in.Code || out.Error != in.Error {
				t.Errorf("round trip mismatch: got %+v, want %+v", out, in)
			}
		})
	}
}

func TestFrameWriterStreamsAndSerializes(t *testing.T) {
	var buf bytes.Buffer
	enc := msgpack.NewEncoder(&buf)
	sender := newFrameSender(enc)
	stdout := newFrameWriter(sender, frameStdout)
	stderr := newFrameWriter(sender, frameStderr)

	// Concurrent writes mimic exec.Cmd potentially writing to both streams.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if _, err := stdout.Write([]byte("o")); err != nil {
				t.Errorf("stdout.Write: %v", err)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if _, err := stderr.Write([]byte("e")); err != nil {
				t.Errorf("stderr.Write: %v", err)
			}
		}
	}()
	wg.Wait()

	dec := msgpack.NewDecoder(&buf)
	var got int
	for {
		var f Frame
		if err := dec.Decode(&f); err != nil {
			break
		}
		if f.Type != frameStdout && f.Type != frameStderr {
			t.Errorf("unexpected frame type %q", f.Type)
		}
		if len(f.Data) != 1 {
			t.Errorf("expected 1-byte data, got %d", len(f.Data))
		}
		got++
	}
	if got != 100 {
		t.Errorf("expected 100 frames, got %d", got)
	}
}
