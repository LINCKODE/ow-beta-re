package audio

import (
	"math"
	"math/rand"
	"testing"
)

func TestIMDCT(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, n := range []int{64, 256, 2048} {
		x := make([]float32, n/2)
		for i := range x {
			x[i] = float32(rng.NormFloat64())
		}
		want := naiveIMDCT(x)
		got := make([]float32, n)
		newIMDCT(n).inverse(x, got)
		var maxErr float64
		for i := range got {
			maxErr = math.Max(maxErr, math.Abs(float64(got[i]-want[i])))
		}
		if maxErr > 1e-3 {
			t.Errorf("n=%d: max error %g (got[0..3] %v want %v)", n, maxErr, got[:4], want[:4])
		}
	}
}
