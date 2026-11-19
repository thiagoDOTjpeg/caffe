package frame

import (
	"bufio"
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	var b bytes.Buffer
	Write(&b, map[string]int{"a": 1})
	Write(&b, "ü")
	r := bufio.NewReader(&b)
	for _, want := range []string{`{"a":1}`, `"ü"`} {
		got, err := Read(r)
		if err != nil || string(got) != want {
			t.Fatalf("%q %v", got, err)
		}
	}
	if _, err := Read(bufio.NewReader(bytes.NewBufferString("X: 1\r\n\r\n"))); err == nil {
		t.Fatal("no length accepted")
	}
}
