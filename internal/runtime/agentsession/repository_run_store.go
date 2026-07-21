package agentsession

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
)

const (
	repositoryRunSlotSize    = 512
	repositoryRunHeaderSize  = 24
	repositoryRunPayloadSize = 88
	repositoryRunVersion     = 2
	repositoryRunEnabledBit  = 1 << iota
	repositoryRunAwaitingBit
)

var repositoryRunCRC = crc32.MakeTable(crc32.Castagnoli)

type repositoryRunSnapshot struct {
	State    repositoryRunState
	Sequence uint64
	Slot     int
}

func readRepositoryRunSnapshot(path string) (repositoryRunSnapshot, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return repositoryRunSnapshot{Slot: -1}, nil
	}
	if err != nil {
		return repositoryRunSnapshot{}, fmt.Errorf("read repository run state: %w", err)
	}
	snapshot, readErr := readRepositoryRunSnapshotFile(file)
	closeErr := file.Close()
	if readErr != nil {
		return repositoryRunSnapshot{}, readErr
	}
	if closeErr != nil {
		return repositoryRunSnapshot{}, fmt.Errorf("close repository run state: %w", closeErr)
	}
	return snapshot, nil
}

func readRepositoryRunSnapshotFile(file *os.File) (repositoryRunSnapshot, error) {
	var buffer [repositoryRunSlotSize*2 + 1]byte
	size, err := file.ReadAt(buffer[:], 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return repositoryRunSnapshot{}, fmt.Errorf("read repository run state: %w", err)
	}
	if size == 0 {
		return repositoryRunSnapshot{Slot: -1}, nil
	}
	if size > repositoryRunSlotSize*2 {
		return repositoryRunSnapshot{}, fmt.Errorf("repository run state exceeds %d bytes", repositoryRunSlotSize*2)
	}
	data := buffer[:size]
	best := repositoryRunSnapshot{Slot: -1}
	valid := false
	for slot := 0; slot < 2; slot++ {
		snapshot, ok := decodeRepositoryRunSlot(data, slot)
		if !ok {
			continue
		}
		if !valid || snapshot.Sequence > best.Sequence {
			best = snapshot
			valid = true
		}
	}
	if !valid {
		return repositoryRunSnapshot{}, fmt.Errorf("repository run state has no valid slot")
	}
	return best, nil
}

func decodeRepositoryRunSlot(data []byte, slot int) (repositoryRunSnapshot, bool) {
	offset := slot * repositoryRunSlotSize
	if len(data) < offset+repositoryRunHeaderSize {
		return repositoryRunSnapshot{}, false
	}
	header := data[offset : offset+repositoryRunHeaderSize]
	if binary.LittleEndian.Uint32(header[:4]) != 0x4e555252 || header[4] != repositoryRunVersion ||
		header[5]&^(repositoryRunEnabledBit|repositoryRunAwaitingBit) != 0 || header[19] != 0 {
		return repositoryRunSnapshot{}, false
	}
	sequence := binary.LittleEndian.Uint64(header[8:16])
	if sequence == 0 {
		return repositoryRunSnapshot{}, false
	}
	payloadSize := int(binary.LittleEndian.Uint16(header[16:18]))
	if payloadSize != repositoryRunPayloadSize || len(data) < offset+repositoryRunHeaderSize+payloadSize {
		return repositoryRunSnapshot{}, false
	}
	payload := data[offset+repositoryRunHeaderSize : offset+repositoryRunHeaderSize+payloadSize]
	if repositoryRunChecksum(header, payload) != binary.LittleEndian.Uint32(header[20:24]) {
		return repositoryRunSnapshot{}, false
	}
	state, ok := decodeRepositoryRunPayload(header, payload)
	if !ok {
		return repositoryRunSnapshot{}, false
	}
	return repositoryRunSnapshot{State: state, Sequence: sequence, Slot: slot}, true
}

func decodeRepositoryRunPayload(header, payload []byte) (repositoryRunState, bool) {
	reason := repositoryRunDisabledReason(header[18])
	if !reason.valid() {
		return repositoryRunState{}, false
	}
	state := repositoryRunState{
		Enabled:              header[5]&repositoryRunEnabledBit != 0,
		AwaitingContinuation: header[5]&repositoryRunAwaitingBit != 0,
		NoProgressNudges:     int(binary.LittleEndian.Uint16(header[6:8])),
		CheckpointMaterial:   binary.LittleEndian.Uint64(payload[:8]),
		EnabledAt:            int64(binary.LittleEndian.Uint64(payload[8:16])),
		LastPolicyCheckpoint: int64(binary.LittleEndian.Uint64(payload[16:24])),
		DisabledReason:       reason,
	}
	copy(state.LastProgressHash[:], payload[24:])
	copy(state.RootIdentity[:], payload[56:88])
	return state, true
}

func writeRepositoryRunSnapshotFile(file *os.File, state repositoryRunState, previous repositoryRunSnapshot) error {
	if previous.Sequence == math.MaxUint64 {
		return fmt.Errorf("repository run state sequence exhausted")
	}
	if !state.DisabledReason.valid() {
		return fmt.Errorf("invalid repository run disabled reason %d", state.DisabledReason)
	}
	if state.NoProgressNudges < 0 {
		state.NoProgressNudges = 0
	}
	if state.NoProgressNudges > math.MaxUint16 {
		return fmt.Errorf("repository run no-progress count exceeds %d", math.MaxUint16)
	}
	sequence := previous.Sequence + 1
	slot := 0
	if previous.Slot == 0 {
		slot = 1
	}
	var record [repositoryRunHeaderSize + repositoryRunPayloadSize]byte
	copy(record[:4], "RRUN")
	record[4] = repositoryRunVersion
	if state.Enabled {
		record[5] |= repositoryRunEnabledBit
	}
	if state.AwaitingContinuation {
		record[5] |= repositoryRunAwaitingBit
	}
	binary.LittleEndian.PutUint16(record[6:8], uint16(state.NoProgressNudges))
	binary.LittleEndian.PutUint64(record[8:16], sequence)
	binary.LittleEndian.PutUint16(record[16:18], repositoryRunPayloadSize)
	record[18] = byte(state.DisabledReason)
	payload := record[repositoryRunHeaderSize:]
	binary.LittleEndian.PutUint64(payload[:8], state.CheckpointMaterial)
	binary.LittleEndian.PutUint64(payload[8:16], uint64(state.EnabledAt))
	binary.LittleEndian.PutUint64(payload[16:24], uint64(state.LastPolicyCheckpoint))
	copy(payload[24:], state.LastProgressHash[:])
	copy(payload[56:88], state.RootIdentity[:])
	binary.LittleEndian.PutUint32(record[20:24], repositoryRunChecksum(record[:repositoryRunHeaderSize], payload))

	written, writeErr := file.WriteAt(record[:], int64(slot*repositoryRunSlotSize))
	if writeErr != nil {
		return fmt.Errorf("write repository run state: %w", writeErr)
	}
	if written != len(record) {
		return fmt.Errorf("write repository run state: wrote %d of %d bytes", written, len(record))
	}
	return nil
}

func repositoryRunChecksum(header, payload []byte) uint32 {
	checksum := crc32.Update(0, repositoryRunCRC, header[:20])
	return crc32.Update(checksum, repositoryRunCRC, payload)
}
