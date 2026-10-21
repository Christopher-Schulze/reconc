package actionstate

import (
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"io"
	"strings"
	"time"

	"reconc.dev/reconc/internal/action"
)

// A store-issued call identity is "act_" followed by 26 base32 characters
// holding 130 bits: the issue second (40 bits), random bits (40), and the
// first 50 bits of a keyed MAC over the repository identity and both fields.
// The issue second therefore leads the identity and is readable without the
// key; only the MAC proves that this store issued it.
const (
	// CallIdentityAdmissionWindow bounds how long after issuance a call
	// identity may enter the state as a new reservation or approval record.
	// Retained history older than the window plus the skew can never be
	// needed to reject a replay, so it is pruned.
	CallIdentityAdmissionWindow = 15 * time.Minute
	// CallIdentityClockSkew tolerates an issuing clock slightly ahead of the
	// clock of the admitting transaction.
	CallIdentityClockSkew = 30 * time.Second

	callIdentityAlphabet  = "abcdefghijklmnopqrstuvwxyz234567"
	callIdentityPrefix    = "act_"
	callIdentityCharacter = 26
	callIdentityBytes     = 17
	callIdentitySeconds   = 5
	callIdentityRandom    = 5
	// callIdentityMACBytes holds the 50 MAC bits: six full bytes and the two
	// high bits of the seventh.
	callIdentityMACBytes  = 7
	callIdentityLastMask  = 0xc0
	maxCallIdentitySecond = 1<<40 - 1
)

// NewCallID issues a call identity bound to this store's key and repository
// at the current trusted time.
func (s *Store) NewCallID() (string, error) {
	if s == nil {
		return "", stateError(action.ReasonStateUnavailable, "action state store is unavailable", nil)
	}
	key, releaseKey, err := s.keyLease.acquireUse()
	if err != nil || key != s.key {
		if releaseKey != nil {
			releaseKey()
		}
		return "", stateError(action.ReasonIdentityUnavailable, "action identity-key lease is inactive", err)
	}
	defer releaseKey()
	snapshot, err := s.clock.Snapshot()
	if err != nil {
		return "", stateError(action.ReasonStateUnavailable, "read trusted action-state clock", err)
	}
	second := snapshot.Time.Unix()
	if second <= 0 || second > maxCallIdentitySecond || !action.SafeLabel(snapshot.Source) {
		return "", stateError(action.ReasonStateUnavailable, "trusted clock snapshot is invalid", nil)
	}
	var raw [callIdentityBytes]byte
	binary.BigEndian.PutUint64(raw[:8], uint64(second)<<24)
	if _, err := io.ReadFull(secureRandomReader, raw[callIdentitySeconds:callIdentitySeconds+callIdentityRandom]); err != nil {
		return "", stateError(action.ReasonIdentityUnavailable, "read call identity entropy", err)
	}
	mac, ok := s.callIdentityMAC(raw[:callIdentitySeconds+callIdentityRandom])
	if !ok {
		return "", stateError(action.ReasonIdentityUnavailable, "derive call identity MAC", nil)
	}
	copy(raw[callIdentitySeconds+callIdentityRandom:], mac[:])
	return encodeCallIdentity(raw), nil
}

// callIdentityMAC returns the 50 MAC bits of fields, padded to seven bytes.
func (s *Store) callIdentityMAC(fields []byte) ([callIdentityMACBytes]byte, bool) {
	var mac [callIdentityMACBytes]byte
	identity := s.key.Identity(DomainCallID, []byte(s.repositoryIdentity), fields)
	separator := strings.LastIndexByte(identity, ':')
	if separator < 0 {
		return mac, false
	}
	digest, err := hex.DecodeString(identity[separator+1:])
	if err != nil || len(digest) < callIdentityMACBytes {
		return mac, false
	}
	copy(mac[:], digest)
	mac[callIdentityMACBytes-1] &= callIdentityLastMask
	return mac, true
}

// callIdentityIssuedAt reads the issue second a call identity encodes. It
// does not prove issuance: retention uses it only to decide when retained
// history can no longer reject an admissible replay.
func callIdentityIssuedAt(callID string) (time.Time, bool) {
	raw, ok := decodeCallIdentity(callID)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(int64(binary.BigEndian.Uint64(raw[:8])>>24), 0), true
}

// issuedCallIdentity reports whether this store's key issued callID.
func (s *Store) issuedCallIdentity(callID string) bool {
	raw, ok := decodeCallIdentity(callID)
	if !ok {
		return false
	}
	mac, ok := s.callIdentityMAC(raw[:callIdentitySeconds+callIdentityRandom])
	return ok && subtle.ConstantTimeCompare(mac[:], raw[callIdentitySeconds+callIdentityRandom:]) == 1
}

// requireAdmissibleCallID accepts a call identity only when this store issued
// it and its issue second lies within the admission window around now.
func (s *Store) requireAdmissibleCallID(callID string, now time.Time) error {
	if !s.issuedCallIdentity(callID) {
		return stateError(action.ReasonIdentityUnavailable, "action call identity was not issued by this action state", nil)
	}
	issued, _ := callIdentityIssuedAt(callID)
	if issued.Before(now.Add(-CallIdentityAdmissionWindow)) || issued.After(now.Add(CallIdentityClockSkew)) {
		return stateError(action.ReasonReservationIndeterminate, "action call identity is outside its admission window", nil)
	}
	return nil
}

// callIdentityExpired reports retained history whose call identity can never
// be admitted again: its issue second lies before the admission window of
// now, and the persisted trusted clock never moves backward.
func callIdentityExpired(callID string, now time.Time) bool {
	issued, ok := callIdentityIssuedAt(callID)
	return ok && issued.Add(CallIdentityAdmissionWindow+CallIdentityClockSkew).Before(now)
}

func encodeCallIdentity(raw [callIdentityBytes]byte) string {
	var out [len(callIdentityPrefix) + callIdentityCharacter]byte
	copy(out[:], callIdentityPrefix)
	for index := range callIdentityCharacter {
		bit := index * 5
		window := uint16(raw[bit/8]) << 8
		if bit/8+1 < len(raw) {
			window |= uint16(raw[bit/8+1])
		}
		value := window >> (11 - bit%8) & 0x1f
		out[len(callIdentityPrefix)+index] = callIdentityAlphabet[value]
	}
	return string(out[:])
}

func decodeCallIdentity(callID string) ([callIdentityBytes]byte, bool) {
	var raw [callIdentityBytes]byte
	if len(callID) != len(callIdentityPrefix)+callIdentityCharacter || !strings.HasPrefix(callID, callIdentityPrefix) {
		return raw, false
	}
	for index := range callIdentityCharacter {
		value := strings.IndexByte(callIdentityAlphabet, callID[len(callIdentityPrefix)+index])
		if value < 0 {
			return raw, false
		}
		bit := index * 5
		window := uint16(value) << (11 - bit%8)
		raw[bit/8] |= byte(window >> 8)
		if bit/8+1 < len(raw) {
			raw[bit/8+1] |= byte(window)
		}
	}
	return raw, true
}
