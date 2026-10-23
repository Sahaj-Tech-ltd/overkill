//go:build windows

package term

import (
	"encoding/binary"
	"testing"
)

func TestProbeRecordCharacter(t *testing.T) {
	key := func(character uint16, repeats uint16) probeInputRecord {
		var record probeInputRecord
		binary.LittleEndian.PutUint16(record[:2], 1)
		binary.LittleEndian.PutUint32(record[4:8], 1)
		binary.LittleEndian.PutUint16(record[8:10], repeats)
		binary.LittleEndian.PutUint16(record[14:16], character)
		return record
	}
	for _, character := range []byte{'\x1b', ']', '1', ';', '\x07', '\\'} {
		got, repeats, ok := probeRecordCharacter(key(uint16(character), 3))
		if !ok || got != character || repeats != 3 {
			t.Fatalf("character %q: got %q, repeats %d, accepted %v", character, got, repeats, ok)
		}
	}
	keyUp := key('a', 1)
	binary.LittleEndian.PutUint32(keyUp[4:8], 0)
	resize := key('a', 1)
	binary.LittleEndian.PutUint16(resize[:2], 4)
	for _, record := range []probeInputRecord{keyUp, resize, key(0, 1), key(0x4e00, 1), key('a', 0)} {
		if _, _, ok := probeRecordCharacter(record); ok {
			t.Fatalf("accepted unrelated or non-ASCII input record: %v", record)
		}
	}
}
