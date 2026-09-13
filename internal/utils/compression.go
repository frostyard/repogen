package utils

import (
	"bytes"
	"io"
	"time"

	"github.com/klauspost/compress/gzip"
)

// GzipCompress compresses data using gzip
func GzipCompress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.ModTime = time.Unix(0, 0).UTC()

	if _, err := w.Write(data); err != nil {
		return nil, err
	}

	if err := w.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// GzipDecompress decompresses gzip data
func GzipDecompress(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()

	return io.ReadAll(r)
}
