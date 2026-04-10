package serverapi

import "testing"

func TestSegmentPathRoundTrip(t *testing.T) {
	p := SegmentPath("c1", 7, 1500)
	id, idx, br, err := ParseSegmentPath(p)
	if err != nil {
		t.Fatal(err)
	}
	if id != "c1" || idx != 7 || br != 1500 {
		t.Fatalf("got %s %d %d", id, idx, br)
	}
}

func TestPayloadSizeDeterministic(t *testing.T) {
	a := PayloadSize("c1", 5, 1500, 4)
	b := PayloadSize("c1", 5, 1500, 4)
	if a != b {
		t.Fatal("non-deterministic")
	}
	if a <= 0 {
		t.Fatal("zero size")
	}
}

func TestFillPayloadDeterministic(t *testing.T) {
	a := make([]byte, 1024)
	b := make([]byte, 1024)
	FillPayload(a, "c1", 0, 1500)
	FillPayload(b, "c1", 0, 1500)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("byte %d differs", i)
		}
	}
}
