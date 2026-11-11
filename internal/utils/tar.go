package utils

import (
	"archive/tar"
	"io"
)

// ReadTarEntry returns the content of the first entry whose name is in names.
// found is false when no entry matches.
func ReadTarEntry(tr *tar.Reader, names ...string) (data []byte, found bool, err error) {
	for {
		header, nextErr := tr.Next()
		if nextErr == io.EOF {
			return nil, false, nil
		}
		if nextErr != nil {
			return nil, false, nextErr
		}

		for _, name := range names {
			if header.Name != name {
				continue
			}
			data, readErr := io.ReadAll(tr)
			if readErr != nil {
				return nil, false, readErr
			}
			return data, true, nil
		}
	}
}
