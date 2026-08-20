package executor

import (
	"strings"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

const (
	codexStreamBootstrapBufferChunkLimit = 12
	codexStreamBootstrapBufferByteLimit  = 64 * 1024
)

type codexStreamBootstrapBuffer struct {
	active bool
	bytes  int
	lines  int
	chunks []cliproxyexecutor.StreamChunk
}

func newCodexStreamBootstrapBuffer() *codexStreamBootstrapBuffer {
	return &codexStreamBootstrapBuffer{active: true}
}

func (b *codexStreamBootstrapBuffer) Active() bool {
	return b != nil && b.active
}

func (b *codexStreamBootstrapBuffer) Buffer(lineBytes int, chunks ...cliproxyexecutor.StreamChunk) []cliproxyexecutor.StreamChunk {
	if b == nil || !b.active {
		return chunks
	}
	b.lines++
	b.bytes += lineBytes
	for i := range chunks {
		b.chunks = append(b.chunks, chunks[i])
	}
	if b.lines < codexStreamBootstrapBufferChunkLimit && b.bytes < codexStreamBootstrapBufferByteLimit {
		return nil
	}
	return b.Commit()
}

func (b *codexStreamBootstrapBuffer) Commit(chunks ...cliproxyexecutor.StreamChunk) []cliproxyexecutor.StreamChunk {
	if b == nil || !b.active {
		return chunks
	}
	flushed := make([]cliproxyexecutor.StreamChunk, 0, len(b.chunks)+len(chunks))
	flushed = append(flushed, b.chunks...)
	flushed = append(flushed, chunks...)
	b.active = false
	b.bytes = 0
	b.lines = 0
	b.chunks = nil
	return flushed
}

func codexErrorIsCredentialAvailabilityNeutral(err error) bool {
	if err == nil {
		return false
	}
	type availabilityNeutralProvider interface {
		IsCredentialAvailabilityNeutral() bool
	}
	provider, ok := err.(availabilityNeutralProvider)
	return ok && provider.IsCredentialAvailabilityNeutral()
}

func codexStreamBootstrapEvent(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "response.created", "response.in_progress", "response.queued":
		return true
	default:
		return false
	}
}

func codexStreamTerminalEvent(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "response.failed", "response.error", "error":
		return true
	default:
		return false
	}
}
