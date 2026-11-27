// Package discovery implements contract.Discovery over UDP: every device
// announces a small beacon every 2 s, and the radar shows whoever was
// heard from in the last 7 s.
package discovery

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/vinayakkam/droplink/internal/contract"
)

// Beacon layout (big endian), at most MaxBeacon bytes:
//
//	"DLNK" | version u8 | avatar u8 | port u16 | nonce u64 |
//	id (32 ASCII hex) | name length u8 | name (UTF-8, ≤ 64 bytes)
//
// nonce is random per run, so a restarted device is noticed at once.
const (
	Magic     = "DLNK"
	Version   = 1
	MaxName   = 64
	fixedLen  = 4 + 1 + 1 + 2 + 8 + 32 + 1
	MaxBeacon = fixedLen + MaxName
)

// ErrBeacon wraps every reason a packet is not a valid beacon.
var ErrBeacon = errors.New("discovery: bad beacon")

// Beacon is what a device announces about itself.
type Beacon struct {
	ID     contract.DeviceID
	Name   string
	Avatar uint8
	Port   uint16 // TCP port of the LAN transport; 0 = not reachable yet
	Nonce  uint64
}

// Encode appends the wire form of b to dst. Names are cut to MaxName
// bytes on a rune boundary.
func (b Beacon) Encode(dst []byte) ([]byte, error) {
	if !validID(b.ID) {
		return dst, fmt.Errorf("%w: id %q", ErrBeacon, b.ID)
	}
	name := truncate(b.Name, MaxName)
	dst = append(dst, Magic...)
	dst = append(dst, Version, b.Avatar&15)
	dst = binary.BigEndian.AppendUint16(dst, b.Port)
	dst = binary.BigEndian.AppendUint64(dst, b.Nonce)
	dst = append(dst, b.ID...)
	dst = append(dst, byte(len(name)))
	return append(dst, name...), nil
}

// Decode parses a packet. It never panics on hostile input.
func Decode(p []byte) (Beacon, error) {
	var b Beacon
	if len(p) < fixedLen || len(p) > MaxBeacon {
		return b, fmt.Errorf("%w: %d bytes", ErrBeacon, len(p))
	}
	if string(p[:4]) != Magic {
		return b, fmt.Errorf("%w: magic", ErrBeacon)
	}
	if p[4] != Version {
		return b, fmt.Errorf("%w: version %d", ErrBeacon, p[4])
	}
	b.Avatar = p[5]
	if b.Avatar > 15 {
		return b, fmt.Errorf("%w: avatar %d", ErrBeacon, b.Avatar)
	}
	b.Port = binary.BigEndian.Uint16(p[6:8])
	b.Nonce = binary.BigEndian.Uint64(p[8:16])
	b.ID = contract.DeviceID(p[16:48])
	if !validID(b.ID) {
		return b, fmt.Errorf("%w: id", ErrBeacon)
	}
	n := int(p[48])
	if n > MaxName || len(p) != fixedLen+n {
		return b, fmt.Errorf("%w: name length", ErrBeacon)
	}
	name := p[fixedLen:]
	if !utf8.Valid(name) {
		return b, fmt.Errorf("%w: name not UTF-8", ErrBeacon)
	}
	for _, r := range string(name) {
		if r < 0x20 || r == 0x7f {
			return b, fmt.Errorf("%w: control character in name", ErrBeacon)
		}
	}
	b.Name = string(name)
	return b, nil
}

func validID(id contract.DeviceID) bool {
	if len(id) != 32 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
