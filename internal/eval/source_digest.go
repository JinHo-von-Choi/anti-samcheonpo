package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
)

func NativeSourceDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() || st.Size() > 32<<20 {
		return "", errors.New("native source must be a bounded regular file")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, (32<<20)+1))
	if err != nil {
		return "", err
	}
	after, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if n > 32<<20 || after.Size() != st.Size() || !after.ModTime().Equal(st.ModTime()) {
		return "", errors.New("native source changed during observation")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
