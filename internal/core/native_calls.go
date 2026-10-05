package core

import (
	"context"
	"time"
)

// CallClient provides one native, two-way voice call at a time. StartCall and
// AnswerCall return after scheduling the operation; CallState reports progress.
// The call lifetime belongs to the connected client, not the command context.
type CallClient interface {
	CallState() CallState
	StartCall(context.Context, Chat) error
	AnswerCall(context.Context, uint64) error
	EndCall(context.Context, uint64) error
	SetCallMuted(uint64, bool) error
}

type CallState struct {
	ID        uint64 // Local generation, also identifies incoming calls.
	Chat      Chat
	Status    string // idle, dialing, ringing, connecting, active, ended, failed
	Incoming  bool
	Muted     bool
	Error     string
	StartedAt time.Time // Set when the media transport connects.
	// Packet counts report transport/queue activity, not audible speaker output.
	SentPackets     uint64
	ReceivedPackets uint64
	// Decoder activity and level do not prove that the output device is audible.
	AudioDecodedFrames uint64
	AudioLevelDB       float64
	AudioLastDecodedAt time.Time
	AudioOutputError   string
}

func (s CallState) Active() bool {
	switch s.Status {
	case "dialing", "ringing", "connecting", "active":
		return true
	default:
		return false
	}
}
