package utils

import (
	"bytes"
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
}
