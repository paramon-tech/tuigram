package platform

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

type opusRTP struct {
	sequence uint16
	payload  []byte
	samples  uint32
}

func parseOpusRTP(packet []byte) (opusRTP, error) {
	var p opusRTP
	if len(packet) < 12 || len(packet) > 65000 || packet[0]>>6 != 2 {
		return p, errors.New("invalid call RTP header")
	}
	header := 12 + 4*int(packet[0]&15)
	if header > len(packet) {
		return p, errors.New("truncated call RTP CSRC list")
	}
	if packet[0]&16 != 0 {
		if header+4 > len(packet) {
			return p, errors.New("truncated call RTP extension")
		}
		header += 4 + 4*int(binary.BigEndian.Uint16(packet[header+2:]))
	}
	end := len(packet)
	if packet[0]&32 != 0 {
		padding := int(packet[end-1])
		if padding == 0 {
			return p, errors.New("invalid call RTP padding")
		}
		end -= padding
	}
	if header >= end {
		return p, errors.New("empty or truncated call RTP payload")
	}
	p.sequence = binary.BigEndian.Uint16(packet[2:4])
	p.payload = packet[header:end]
	samples, err := opusSamples(p.payload)
	if err != nil {
		return opusRTP{}, err
	}
	p.samples = samples
	return p, nil
}

// RFC 6716 section 3.1: Opus TOC gives frame duration and count. Ogg granules
// always use a 48 kHz clock, including packets that encode narrower bandwidth.
func opusSamples(packet []byte) (uint32, error) {
	if len(packet) == 0 {
		return 0, errors.New("empty Opus packet")
	}
	toc := packet[0]
	var samples uint32
	switch {
	case toc&0x80 != 0:
		samples = 120 << ((toc >> 3) & 3)
	case toc&0x60 == 0x60:
		samples = 480 << ((toc >> 3) & 1)
	default:
		samples = 480 << ((toc >> 3) & 3)
		if samples == 3840 {
			samples = 2880
		}
	}
	frames := uint32(1)
	switch toc & 3 {
	case 1, 2:
		frames = 2
	case 3:
		if len(packet) < 2 {
			return 0, errors.New("truncated Opus frame count")
		}
		frames = uint32(packet[1] & 63)
	}
	if frames == 0 || samples*frames > 5760 {
		return 0, errors.New("invalid Opus packet duration")
	}
	return samples * frames, nil
}

// opusPages wraps decrypted RTP payloads in an in-memory RFC 7845 stream. Only
// the local player's pipe sees it. One packet per page keeps playback latency
// low; the headers declare stereo so either mono or stereo remote Opus works.
type opusPages struct {
	writer  io.Writer
	page    uint32
	granule uint64
}

func (o *opusPages) headers() error {
	head := make([]byte, 19)
	copy(head, "OpusHead")
	head[8], head[9] = 1, 2
	// Live RTP has no encoder pre-skip to remove.
	binary.LittleEndian.PutUint32(head[12:16], 48000)
	if err := o.writePage(head, 2); err != nil {
		return err
	}
	tags := make([]byte, 23)
	copy(tags, "OpusTags")
	binary.LittleEndian.PutUint32(tags[8:12], 7)
	copy(tags[12:19], "tuigram")
	return o.writePage(tags, 0)
}

func (o *opusPages) packet(p opusRTP) error {
	o.granule += uint64(p.samples)
	return o.writePage(p.payload, 0)
}

func (o *opusPages) writePage(packet []byte, flags byte) error {
	segments := len(packet)/255 + 1
	if segments > 255 {
		return errors.New("Opus packet exceeds Ogg page capacity")
	}
	page := make([]byte, 27+segments+len(packet))
	copy(page, "OggS")
	page[5] = flags
	binary.LittleEndian.PutUint64(page[6:14], o.granule)
	binary.LittleEndian.PutUint32(page[14:18], 1)
	binary.LittleEndian.PutUint32(page[18:22], o.page)
	page[26] = byte(segments)
	for i := 0; i < segments-1; i++ {
		page[27+i] = 255
	}
	page[27+segments-1] = byte(len(packet) % 255)
	copy(page[27+segments:], packet)
	var checksum uint32
	for _, b := range page {
		checksum ^= uint32(b) << 24
		for i := 0; i < 8; i++ {
			if checksum&0x80000000 != 0 {
				checksum = (checksum << 1) ^ 0x04c11db7
			} else {
				checksum <<= 1
			}
		}
	}
	binary.LittleEndian.PutUint32(page[22:26], checksum)
	if _, err := o.writer.Write(page); err != nil {
		return err
	}
	o.page++
	return nil
}

// reorderOpus tolerates up to 60 ms of network reordering with at most 16
// packets retained. Missing packets are skipped after that deadline; repeated
// or late packets are dropped, including across sequence-number wraparound.
type reorderOpus struct {
	started bool
	next    uint16
	pending map[uint16]opusRTP
	waiting time.Time
}

func (r *reorderOpus) push(p opusRTP, now time.Time, write func(opusRTP) error) error {
	if !r.started {
		r.started, r.next, r.pending = true, p.sequence, make(map[uint16]opusRTP)
	}
	distance := int16(p.sequence - r.next)
	if distance < 0 {
		return nil
	}
	if distance > 64 || len(r.pending) >= 16 {
		clear(r.pending)
		r.next = p.sequence
	}
	r.pending[p.sequence] = p
	return r.drain(now, write)
}

func (r *reorderOpus) drain(now time.Time, write func(opusRTP) error) error {
	for len(r.pending) > 0 {
		p, ok := r.pending[r.next]
		if !ok {
			if r.waiting.IsZero() {
				r.waiting = now
			}
			if now.Sub(r.waiting) < 60*time.Millisecond {
				return nil
			}
			closest := uint16(65535)
			for seq := range r.pending {
				if distance := seq - r.next; distance < closest {
					closest = distance
				}
			}
			r.next += closest
			continue
		}
		delete(r.pending, r.next)
		r.next++
		r.waiting = time.Time{}
		if err := write(p); err != nil {
			return err
		}
	}
	return nil
}

func (a *CallAudio) playback() {
	o := &opusPages{writer: a.input}
	if err := o.headers(); err != nil {
		if a.ctx.Err() == nil {
			a.fail(fmt.Errorf("initialize call speaker: %w", err))
		}
		return
	}
	var reorder reorderOpus
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		var err error
		select {
		case <-a.ctx.Done():
			return
		case p := <-a.incoming:
			err = reorder.push(p, time.Now(), o.packet)
		case now := <-tick.C:
			err = reorder.drain(now, o.packet)
		}
		if err != nil {
			if a.ctx.Err() == nil {
				a.fail(fmt.Errorf("play call audio: %w", err))
			}
			return
		}
	}
}
