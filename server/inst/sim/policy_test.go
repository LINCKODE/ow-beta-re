package sim

import "testing"

func TestLoadEvidence(t *testing.T) {
	var p Policy
	n, err := p.LoadEvidence([]byte(`{"states": {"0x304:54": {"mismatches": 3}, "0x304:0": {"mismatches": 0}, "0x15:13": {"mismatches": 1}}}`))
	if err != nil || n != 2 {
		t.Fatalf("n %d err %v", n, err)
	}
	if !p.States[[2]int{0x304, 54}] || p.States[[2]int{0x304, 0}] || !p.States[[2]int{0x15, 13}] {
		t.Errorf("states %v", p.States)
	}
	if _, err := p.LoadEvidence([]byte(`{"states": {"bad": {}}}`)); err == nil {
		t.Error("bad key accepted")
	}
}
