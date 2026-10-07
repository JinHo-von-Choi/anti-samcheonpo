package sources

import (
	"os"
	"testing"
)

func BenchmarkParseLargest(b *testing.B) {
	p := os.Getenv("SAMCHEONPO_BENCH_FILE")
	if p == "" {
		b.Skip("SAMCHEONPO_BENCH_FILE not set")
	}
	st, _ := os.Stat(p)
	b.SetBytes(st.Size())
	for i := 0; i < b.N; i++ {
		if _, err := Parse(File{Path: p, Agent: DetectAgent(p)}); err != nil {
			b.Fatal(err)
		}
	}
}
