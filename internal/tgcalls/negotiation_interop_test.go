package calls

import "testing"

// Telegram's ContentNegotiationContext::getAnswer echoes the offer's SSRC.
// The answer accepts our send channel; it is not the peer's receive channel.
func TestOfficialAnswerDoesNotBecomeIncomingSSRC(t *testing.T) {
	n := newContentNegotiation(true)
	offer := n.proposeChannels(1000, 1001)
	answer := &negotiateChannelsMessage{
		Type: typeNegotiateChannels, ExchangeID: offer.ExchangeID,
		Contents: []mediaContent{audioContent(1000), videoContent(1001)},
	}
	reply, ready := n.applyRemoteChannels(answer, 1000, 1001)
	if reply != nil || ready || n.peerAudioSSRC() != 0 || n.peerVideoSSRC() != 0 {
		t.Fatalf("our outgoing answer became incoming media: reply=%v ready=%v peer=%d/%d", reply, ready, n.peerAudioSSRC(), n.peerVideoSSRC())
	}
}

func TestOfficialOfferAnswerEchoesPeerSSRC(t *testing.T) {
	n := newContentNegotiation(false)
	remote := &negotiateChannelsMessage{
		Type: typeNegotiateChannels, ExchangeID: "999",
		Contents: []mediaContent{audioContent(5000)},
	}
	reply, _ := n.applyRemoteChannels(remote, 1000, 1001)
	if reply == nil || len(reply.Contents) != 1 || reply.Contents[0].Ssrc != "5000" {
		t.Fatalf("answer did not echo the peer's send SSRC: %+v", reply)
	}
}

func TestOfficialCallerAnswerThenIndependentPeerOffer(t *testing.T) {
	n := newContentNegotiation(true)
	local := n.proposeChannels(1000, 1001)
	answer := &negotiateChannelsMessage{Type: typeNegotiateChannels, ExchangeID: local.ExchangeID, Contents: []mediaContent{audioContent(1000)}}
	if _, ready := n.applyRemoteChannels(answer, 1000, 1001); ready {
		t.Fatal("outgoing acceptance alone created an incoming channel")
	}
	remote := &negotiateChannelsMessage{Type: typeNegotiateChannels, ExchangeID: "peer-offer", Contents: []mediaContent{audioContent(5000)}}
	reply, ready := n.applyRemoteChannels(remote, 1000, 1001)
	if !ready || reply == nil || reply.ExchangeID != remote.ExchangeID || reply.Contents[0].Ssrc != "5000" || n.peerAudioSSRC() != 5000 {
		t.Fatalf("two independent directions did not negotiate: reply=%+v ready=%v peer=%d", reply, ready, n.peerAudioSSRC())
	}
	// A repeated answer must neither replace the receive SSRC nor create a reply.
	if reply, ready = n.applyRemoteChannels(answer, 1000, 1001); reply != nil || !ready || n.peerAudioSSRC() != 5000 {
		t.Fatal("repeated outgoing answer corrupted the incoming channel")
	}
}

func TestOfficialCalleeOfferBeforeOwnAnswer(t *testing.T) {
	n := newContentNegotiation(false)
	if n.proposeChannels(1000, 1001) != nil {
		t.Fatal("callee offered before receiving the caller's channels")
	}
	callerOffer := &negotiateChannelsMessage{Type: typeNegotiateChannels, ExchangeID: "caller-offer", Contents: []mediaContent{audioContent(5000)}}
	answer, ready := n.applyRemoteChannels(callerOffer, 1000, 1001)
	if answer == nil || answer.Contents[0].Ssrc != "5000" || ready {
		t.Fatal("callee must first acknowledge the caller's send channel")
	}
	local := n.proposeChannels(1000, 1001)
	if local == nil || local.ExchangeID == callerOffer.ExchangeID || local.Contents[0].Ssrc != "1000" {
		t.Fatal("callee did not create an independent send offer")
	}
	// The callee's own answer may arrive after the receive channel is known.
	answer = &negotiateChannelsMessage{Type: typeNegotiateChannels, ExchangeID: local.ExchangeID, Contents: []mediaContent{audioContent(1000)}}
	if reply, ready := n.applyRemoteChannels(answer, 1000, 1001); reply != nil || !ready || n.peerAudioSSRC() != 5000 {
		t.Fatal("callee did not complete both media directions")
	}
}

func TestOfficialCallerWinsSimultaneousOffer(t *testing.T) {
	n := newContentNegotiation(true)
	local := n.proposeChannels(1000, 1001)
	remote := &negotiateChannelsMessage{Type: typeNegotiateChannels, ExchangeID: "losing-peer-offer", Contents: []mediaContent{audioContent(5000)}}
	if reply, ready := n.applyRemoteChannels(remote, 1000, 1001); reply != nil || ready || n.peerAudioSSRC() != 0 || n.localExchangeID != local.ExchangeID {
		t.Fatal("caller abandoned its winning offer during glare")
	}
	ack := &negotiateChannelsMessage{Type: typeNegotiateChannels, ExchangeID: local.ExchangeID, Contents: []mediaContent{audioContent(1000)}}
	n.applyRemoteChannels(ack, 1000, 1001)
	remote.ExchangeID = "peer-retry"
	if reply, ready := n.applyRemoteChannels(remote, 1000, 1001); reply == nil || !ready || n.peerAudioSSRC() != 5000 {
		t.Fatal("peer retry after winning offer's ACK was not accepted")
	}
}

func TestOfficialCalleeRetriesGlareAndIgnoresAbandonedAnswer(t *testing.T) {
	n := newContentNegotiation(false)
	// Simulate an already-pending callee offer when the caller's winning offer
	// arrives (for example, renegotiation from an older compatible endpoint).
	n.offered, n.localExchangeID = true, "abandoned"
	remote := &negotiateChannelsMessage{Type: typeNegotiateChannels, ExchangeID: "caller-wins", Contents: []mediaContent{audioContent(5000)}}
	if reply, ready := n.applyRemoteChannels(remote, 1000, 1001); reply == nil || ready || n.offered {
		t.Fatal("callee failed to abandon its pending offer and answer the caller")
	}
	replacement := n.proposeChannels(1000, 1001)
	if replacement == nil || replacement.ExchangeID == "abandoned" {
		t.Fatal("callee did not retry its own media direction")
	}
	lateAnswer := &negotiateChannelsMessage{Type: typeNegotiateChannels, ExchangeID: "abandoned", Contents: []mediaContent{audioContent(1000)}}
	if reply, ready := n.applyRemoteChannels(lateAnswer, 1000, 1001); reply != nil || ready || !n.offered || n.localExchangeID != replacement.ExchangeID || n.peerAudioSSRC() != 5000 {
		t.Fatal("abandoned answer canceled the replacement offer or became incoming audio")
	}
	// Retransmitted remote offers are not new glare. Re-answer the same offer
	// without changing the still-pending local exchange.
	if reply, ready := n.applyRemoteChannels(remote, 1000, 1001); reply == nil || ready || !n.offered || n.localExchangeID != replacement.ExchangeID {
		t.Fatal("duplicate caller offer canceled the replacement local offer")
	}
	ack := &negotiateChannelsMessage{Type: typeNegotiateChannels, ExchangeID: replacement.ExchangeID, Contents: []mediaContent{audioContent(1000)}}
	if reply, ready := n.applyRemoteChannels(ack, 1000, 1001); reply != nil || !ready || n.peerAudioSSRC() != 5000 {
		t.Fatal("replacement exchange did not complete")
	}
}
