package calls

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/go-faster/errors"
)

// tgcalls JSON signaling message types (the "@type" discriminator).
const (
	typeInitialSetup      = "InitialSetup"
	typeCandidates        = "Candidates"
	typeMediaState        = "MediaState"
	typeNegotiateChannels = "NegotiateChannels"
)

type sigFingerprint struct {
	Hash        string `json:"hash"`
	Setup       string `json:"setup"`
	Fingerprint string `json:"fingerprint"`
}

type sigFeedback struct {
	Type string `json:"type"`
	// Subtype must always be serialized (even empty): tgcalls' FeedbackType
	// parser rejects the whole message if the "subtype" key is missing, which
	// would make the peer ignore our channel negotiation entirely.
	Subtype string `json:"subtype"`
}

type sigPayloadType struct {
	ID            int               `json:"id"`
	Name          string            `json:"name"`
	Clockrate     int               `json:"clockrate"`
	Channels      int               `json:"channels,omitempty"`
	FeedbackTypes []sigFeedback     `json:"feedbackTypes,omitempty"`
	Parameters    map[string]string `json:"parameters,omitempty"`
}

type sigSsrcGroup struct {
	Semantics string   `json:"semantics"`
	Ssrcs     []string `json:"ssrcs"`
}

type sigExtension struct {
	ID  int    `json:"id"`
	URI string `json:"uri"`
}

// initialSetupMessage carries the local ICE credentials and DTLS fingerprint.
type initialSetupMessage struct {
	Type         string           `json:"@type"`
	Ufrag        string           `json:"ufrag"`
	Pwd          string           `json:"pwd"`
	Renomination bool             `json:"renomination"`
	Fingerprints []sigFingerprint `json:"fingerprints"`
}

type candidateDescription struct {
	SdpString string `json:"sdpString"`
}

// candidatesMessage carries trickled ICE candidates.
type candidatesMessage struct {
	Type       string                 `json:"@type"`
	Candidates []candidateDescription `json:"candidates"`
}

// mediaStateMessage reports mute/video state to the peer.
type mediaStateMessage struct {
	Type            string `json:"@type"`
	Muted           bool   `json:"muted"`
	LowBattery      bool   `json:"lowBattery"`
	VideoState      string `json:"videoState"`
	VideoRotation   int    `json:"videoRotation"`
	ScreencastState string `json:"screencastState"`
}

type mediaContent struct {
	Type          string           `json:"type"`
	Ssrc          string           `json:"ssrc"`
	SsrcGroups    []sigSsrcGroup   `json:"ssrcGroups,omitempty"`
	PayloadTypes  []sigPayloadType `json:"payloadTypes"`
	RtpExtensions []sigExtension   `json:"rtpExtensions,omitempty"`
}

// negotiateChannelsMessage exchanges SSRCs and codec parameters for the media tracks.
type negotiateChannelsMessage struct {
	Type       string         `json:"@type"`
	ExchangeID string         `json:"exchangeId"`
	Contents   []mediaContent `json:"contents"`
}

// envelope is used to peek at the message type before full decoding.
type envelope struct {
	Type string `json:"@type"`
}

// signalingType extracts the "@type" of a raw JSON control message.
func signalingType(data []byte) (string, error) {
	var e envelope
	if err := json.Unmarshal(data, &e); err != nil {
		return "", errors.Wrap(err, "decode envelope")
	}
	if e.Type == "" {
		return "", errors.New("signaling message missing @type")
	}
	return e.Type, nil
}

// contentNegotiation tracks the NegotiateChannels handshake and the peer's
// SSRCs, mirroring tgcalls' ContentNegotiationContext.
type contentNegotiation struct {
	isOutgoing      bool
	localExchangeID string
	offered         bool
	accepted        bool
	peerExchangeID  string

	peerAudio uint32
	peerVideo uint32
}

func newContentNegotiation(isOutgoing bool) *contentNegotiation {
	return &contentNegotiation{isOutgoing: isOutgoing}
}

func (n *contentNegotiation) peerAudioSSRC() uint32 { return n.peerAudio }
func (n *contentNegotiation) peerVideoSSRC() uint32 { return n.peerVideo }

// The caller offers first. The callee answers that offer before advertising its
// own independent send channels. Each direction has a separate exchange ID.
func (n *contentNegotiation) proposeChannels(audioSSRC, videoSSRC uint32) *negotiateChannelsMessage {
	if n.offered || (!n.isOutgoing && n.peerAudio == 0) {
		return nil
	}
	n.offered = true
	n.localExchangeID = randomExchangeID()
	return &negotiateChannelsMessage{
		Type:       typeNegotiateChannels,
		ExchangeID: n.localExchangeID,
		Contents:   []mediaContent{audioContent(audioSSRC), videoContent(videoSSRC)},
	}
}

// Answers acknowledge the offerer's SSRCs. Only an independent peer offer
// supplies incoming SSRCs; treating an echoed answer as a peer offer connects
// the receiver to our own SSRC and silently loses all incoming audio.
func (n *contentNegotiation) applyRemoteChannels(msg *negotiateChannelsMessage, audioSSRC, videoSSRC uint32) (reply *negotiateChannelsMessage, ready bool) {
	if n.offered && msg.ExchangeID == n.localExchangeID {
		for _, content := range msg.Contents {
			if content.Type == "audio" && parseSSRC(content.Ssrc) == audioSSRC {
				if _, ok := supportedContent(content); ok {
					n.accepted = true
				}
			}
		}
		return nil, n.ready()
	}
	// A delayed answer for an abandoned local exchange still contains only
	// our SSRCs. Ignore it before glare handling, so it cannot cancel a newer
	// local offer or become an incoming stream.
	hasPeerContent := false
	for _, content := range msg.Contents {
		ssrc := parseSSRC(content.Ssrc)
		if ssrc != 0 && ssrc != audioSSRC && ssrc != videoSSRC {
			if _, ok := supportedContent(content); ok {
				hasPeerContent = true
				break
			}
		}
	}
	if !hasPeerContent {
		return nil, n.ready()
	}
	if n.offered && !n.accepted && msg.ExchangeID != n.peerExchangeID {
		// Match tgcalls' glare rule: the call initiator's offer wins. A
		// callee cancels its pending offer and advertises it again after
		// answering the caller. A caller waits for its existing answer.
		if n.isOutgoing {
			return nil, n.ready()
		}
		n.offered = false
		n.localExchangeID = ""
	}
	var accepted []mediaContent
	for _, content := range msg.Contents {
		ssrc := parseSSRC(content.Ssrc)
		if ssrc == 0 || ssrc == audioSSRC || ssrc == videoSSRC {
			continue
		}
		content, ok := supportedContent(content)
		if !ok {
			continue
		}
		switch content.Type {
		case "audio":
			if n.peerAudio == 0 || n.peerAudio == ssrc {
				n.peerAudio = ssrc
				accepted = append(accepted, content)
			}
		case "video":
			if n.peerVideo == 0 || n.peerVideo == ssrc {
				n.peerVideo = ssrc
				accepted = append(accepted, content)
			}
		}
	}
	if len(accepted) == 0 {
		return nil, n.ready()
	}
	n.peerExchangeID = msg.ExchangeID
	return &negotiateChannelsMessage{
		Type:       typeNegotiateChannels,
		ExchangeID: msg.ExchangeID,
		Contents:   accepted,
	}, n.ready()
}

func (n *contentNegotiation) ready() bool { return n.accepted && n.peerAudio != 0 }

// Keep the peer's SSRC/groups and supported codec parameters in the answer.
// Payload IDs must match the codecs registered by buildMediaEngine; this is
// the same Opus/VP8 subset this transport advertised before the interop fix.
func supportedContent(content mediaContent) (mediaContent, bool) {
	var payloads []sigPayloadType
	for _, payload := range content.PayloadTypes {
		if (content.Type == "audio" && strings.EqualFold(payload.Name, "opus") && payload.ID == 111 && payload.Clockrate == 48000 && payload.Channels == 2) ||
			(content.Type == "video" && strings.EqualFold(payload.Name, "VP8") && payload.ID == 100 && payload.Clockrate == 90000) {
			payloads = append(payloads, payload)
		}
	}
	content.PayloadTypes = payloads
	return content, len(payloads) != 0
}

func audioContent(ssrc uint32) mediaContent {
	return mediaContent{
		Type: "audio",
		Ssrc: ssrcString(ssrc),
		PayloadTypes: []sigPayloadType{{
			ID: 111, Name: "opus", Clockrate: 48000, Channels: 2,
			FeedbackTypes: []sigFeedback{{Type: "transport-cc"}},
			Parameters:    map[string]string{"minptime": "10", "useinbandfec": "1"},
		}},
		RtpExtensions: rtpExtensions(),
	}
}

func videoContent(ssrc uint32) mediaContent {
	return mediaContent{
		Type: "video",
		Ssrc: ssrcString(ssrc),
		SsrcGroups: []sigSsrcGroup{{
			Semantics: "FID",
			Ssrcs:     []string{ssrcString(ssrc), ssrcString(ssrc + 1)},
		}},
		PayloadTypes: []sigPayloadType{{
			ID: 100, Name: "VP8", Clockrate: 90000,
			FeedbackTypes: []sigFeedback{
				{Type: "goog-remb"}, {Type: "transport-cc"},
				{Type: "ccm", Subtype: "fir"},
				{Type: "nack"}, {Type: "nack", Subtype: "pli"},
			},
		}},
		RtpExtensions: rtpExtensions(),
	}
}

func rtpExtensions() []sigExtension {
	return []sigExtension{
		{ID: 2, URI: "http://www.webrtc.org/experiments/rtp-hdrext/abs-send-time"},
		{ID: 3, URI: "http://www.ietf.org/id/draft-holmer-rmcat-transport-wide-cc-extensions-01"},
	}
}

func ssrcString(ssrc uint32) string { return strconv.FormatUint(uint64(ssrc), 10) }

func parseSSRC(s string) uint32 {
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0
	}
	return uint32(v)
}

func randomExchangeID() string {
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	id := binary.BigEndian.Uint32(buf[:]) & 0x7fffffff
	if id == 0 {
		id = 1
	}
	return strconv.FormatUint(uint64(id), 10)
}
