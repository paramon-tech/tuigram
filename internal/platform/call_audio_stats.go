package platform

import (
	"bytes"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The player exports only a scalar level for each decoded frame. Audio samples
// remain in ffplay; the application never records or persists received audio.
const callOutputFilter = `astats=metadata=1:reset=1:measure_perchannel=none:measure_overall=RMS_level,ametadata=mode=print:key=lavfi.astats.Overall.RMS_level:file='pipe\:1':direct=1`

// CallOutputState reports decoding inside the live player, not whether a sound
// physically reached the speakers. LevelDB is RMS dBFS; -Inf means silence.
type CallOutputState struct {
	DecodedFrames uint64
	LevelDB       float64
	LastDecodedAt time.Time
	Error         string
}

type callOutputMonitor struct {
	mu      sync.RWMutex
	state   CallOutputState
	pending []byte
	discard bool
	stderr  audioErrorTail
}

// OutputState returns a concurrent snapshot suitable for call controls. The
// bounded error tail also exposes decoder errors that do not terminate ffplay.
func (a *CallAudio) OutputState() CallOutputState {
	a.output.mu.RLock()
	state := a.output.state
	a.output.mu.RUnlock()
	state.Error = a.output.stderr.String()
	return state
}

// Write handles fragmented metadata writes with bounded memory. It cannot
// block playback on a UI reader, and rejects malformed levels rather than
// reporting them as successfully decoded sound.
func (m *callOutputMonitor) Write(p []byte) (int, error) {
	n := len(p)
	m.mu.Lock()
	defer m.mu.Unlock()
	for len(p) > 0 {
		end := bytes.IndexByte(p, '\n')
		part := p
		if end >= 0 {
			part = p[:end]
		}
		if !m.discard {
			if len(m.pending)+len(part) > 512 {
				m.pending = m.pending[:0]
				m.discard = true
			} else {
				m.pending = append(m.pending, part...)
			}
		}
		if end < 0 {
			break
		}
		if !m.discard {
			if value, ok := strings.CutPrefix(string(m.pending), "lavfi.astats.Overall.RMS_level="); ok {
				level, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
				if err == nil && !math.IsNaN(level) && !math.IsInf(level, 1) {
					m.state.DecodedFrames++
					m.state.LevelDB = level
					m.state.LastDecodedAt = time.Now()
				}
			}
		}
		m.pending, m.discard = m.pending[:0], false
		p = p[end+1:]
	}
	return n, nil
}
