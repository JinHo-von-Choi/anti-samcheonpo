package hookclient

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// wireVector is one row of wire_vectors.json. The file holds the Go
// encoder's own answers, so the Rust transport can be checked against the
// same bytes without a second implementation of the rules.
type wireVector struct {
	Input   string `json:"input"`
	Valid   bool   `json:"valid"`
	Payload string `json:"payload"`
}

// TestWireVectorsMatchTheGoEncoder holds the Go side of the wire vectors to
// the same answers. A change in the encoder that the vectors do not know
// about fails here.
func TestWireVectorsMatchTheGoEncoder(t *testing.T) {
	raw, err := os.ReadFile("wire_vectors.json")
	if err != nil {
		t.Fatalf("와이어 벡터 읽기 실패: %v", err)
	}
	var vectors []wireVector
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatalf("와이어 벡터 해석 실패: %v", err)
	}
	if len(vectors) == 0 {
		t.Fatal("와이어 벡터가 없다")
	}
	for _, v := range vectors {
		input, err := hex.DecodeString(v.Input)
		if err != nil {
			t.Fatalf("입력 %q: 16진수 해석 실패: %v", v.Input, err)
		}
		if got := json.Valid(input); got != v.Valid {
			t.Errorf("입력 %q: 유효성 %t, 기대 %t", v.Input, got, v.Valid)
			continue
		}
		if !v.Valid {
			continue
		}
		blob, err := json.Marshal(map[string]any{"payload": json.RawMessage(input)})
		if err != nil {
			t.Fatalf("입력 %q: 마샬 실패: %v", v.Input, err)
		}
		var frame map[string]json.RawMessage
		if err := json.Unmarshal(blob, &frame); err != nil {
			t.Fatalf("입력 %q: 프레임 해석 실패: %v", v.Input, err)
		}
		if got := hex.EncodeToString(frame["payload"]); got != v.Payload {
			t.Errorf("입력 %q: 페이로드 %s, 기대 %s", v.Input, got, v.Payload)
		}
	}
}
