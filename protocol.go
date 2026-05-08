package main

import (
	"sync"

	"github.com/vmihailenco/msgpack/v5"
)

// Frame types in the response stream.
const (
	frameStdout = "stdout"
	frameStderr = "stderr"
	frameLog    = "log"
	frameExit   = "exit"
)

// Verb names in the request.
const (
	verbRun = "run"
)

// Request is sent once by the client at the start of a connection.
type Request struct {
	Verb  string `msgpack:"verb"`
	Job   string `msgpack:"job"`
	Force bool   `msgpack:"force,omitempty"`
}

// Frame is one element of the response stream. Exactly one payload field is
// populated per frame, determined by Type.
type Frame struct {
	Type  string `msgpack:"type"`
	Data  []byte `msgpack:"data,omitempty"`
	Msg   string `msgpack:"msg,omitempty"`
	Code  int    `msgpack:"code,omitempty"`
	Error string `msgpack:"error,omitempty"`
}

// frameSender serializes access to a shared msgpack encoder so the runner's
// stdout and stderr can be written from concurrent goroutines safely.
type frameSender struct {
	enc *msgpack.Encoder
	mu  sync.Mutex
}

func newFrameSender(enc *msgpack.Encoder) *frameSender {
	return &frameSender{enc: enc}
}

func (s *frameSender) send(frame Frame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enc.Encode(frame)
}

// frameWriter is an io.Writer that wraps each Write as a Frame of the given
// type. Multiple frameWriters can share one frameSender to interleave types
// safely on the same connection.
type frameWriter struct {
	sender    *frameSender
	frameType string
}

func newFrameWriter(sender *frameSender, frameType string) *frameWriter {
	return &frameWriter{sender: sender, frameType: frameType}
}

func (w *frameWriter) Write(p []byte) (int, error) {
	// Copy the slice; msgpack marshals []byte by reference but the caller
	// may reuse the buffer once Write returns.
	data := make([]byte, len(p))
	copy(data, p)

	if err := w.sender.send(Frame{Type: w.frameType, Data: data}); err != nil {
		return 0, err
	}

	return len(p), nil
}
