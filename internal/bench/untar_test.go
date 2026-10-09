package bench

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func tarOf(t *testing.T, entries ...tar.Header) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range entries {
		h := h
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len("data"))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte("data")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes())
}

func TestExtractTarUnpacksFilesAndKeepsExecutableMode(t *testing.T) {
	dir := t.TempDir()
	err := extractTar(tarOf(t,
		tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header"},
		tar.Header{Typeflag: tar.TypeDir, Name: "src/", Mode: 0o755},
		tar.Header{Typeflag: tar.TypeReg, Name: "src/a.py", Mode: 0o644},
		tar.Header{Typeflag: tar.TypeReg, Name: "bin/run.sh", Mode: 0o755},
	), dir)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "src", "a.py")); err != nil || string(b) != "data" {
		t.Fatalf("file content: %q %v", b, err)
	}
	st, err := os.Stat(filepath.Join(dir, "bin", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&0o111 == 0 && filepath.Separator == '/' {
		t.Fatal("the executable bit was lost")
	}
}

func TestExtractTarRefusesEntriesOutsideTheFolder(t *testing.T) {
	for _, name := range []string{"../escape.txt", "/etc/passwd", "a/../../escape.txt"} {
		dir := t.TempDir()
		if err := extractTar(tarOf(t, tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o644}), dir); err == nil {
			t.Errorf("%q was unpacked", name)
		}
	}
}
