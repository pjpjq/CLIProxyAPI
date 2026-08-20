package executor

import (
	"testing"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func TestCodexStreamBootstrapBufferIsBounded(t *testing.T) {
	buffer := newCodexStreamBootstrapBuffer()
	var flushed []cliproxyexecutor.StreamChunk
	for i := 0; i < codexStreamBootstrapBufferChunkLimit; i++ {
		flushed = buffer.Buffer(1, cliproxyexecutor.StreamChunk{Payload: []byte("x")})
	}
	if buffer.Active() {
		t.Fatal("bootstrap buffer stayed active after the line limit")
	}
	if len(flushed) != codexStreamBootstrapBufferChunkLimit {
		t.Fatalf("flushed chunks = %d, want %d", len(flushed), codexStreamBootstrapBufferChunkLimit)
	}

	byteBuffer := newCodexStreamBootstrapBuffer()
	flushed = byteBuffer.Buffer(codexStreamBootstrapBufferByteLimit, cliproxyexecutor.StreamChunk{Payload: []byte("control")})
	if byteBuffer.Active() || len(flushed) != 1 {
		t.Fatalf("byte-limited flush = active %t chunks %d", byteBuffer.Active(), len(flushed))
	}
}
