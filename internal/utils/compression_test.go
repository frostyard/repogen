package utils

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestGzipCompressDeterministic(t *testing.T) {
	input := []byte("identical input")
	first, err := GzipCompress(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := GzipCompress(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("identical input produced different gzip bytes")
	}
	if len(first) < 8 {
		t.Fatalf("gzip output is too short: %d bytes", len(first))
	}
	if modTime := binary.LittleEndian.Uint32(first[4:8]); modTime != 0 {
		t.Fatalf("gzip MTIME = %d, want 0", modTime)
	}
}
