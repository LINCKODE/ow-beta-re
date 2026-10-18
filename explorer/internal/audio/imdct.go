package audio

import (
	"math"
	"math/cmplx"
)

// imdct computes y[n] = Σ_k X[k] cos(2π/N (n + 1/2 + N/4)(k + 1/2)), n < N, k < N/2, through a DCT-IV of size N/2,
// which is computed with an N/4-point complex FFT.
type imdct struct {
	n    int
	pre  []complex128 // exp(-iπ(k + 1/4)/M), M = N/2
	post []complex128 // exp(-iπ j/M)
	fft  *fft
	z, u []complex128
	dct  []float64
}

func newIMDCT(n int) *imdct {
	m := n / 2
	t := &imdct{n: n, fft: newFFT(m / 2), z: make([]complex128, m/2), dct: make([]float64, m)}
	for k := range m / 2 {
		t.pre = append(t.pre, cmplx.Exp(complex(0, -math.Pi*(float64(k)+0.25)/float64(m))))
		t.post = append(t.post, cmplx.Exp(complex(0, -math.Pi*float64(k)/float64(m))))
	}
	return t
}

// dct4 computes u[m] = Σ_k x[k] cos(π/M (m + 1/2)(k + 1/2)).
func (t *imdct) dct4(x []float32) []float64 {
	m := len(x)
	for k := range m / 2 {
		t.z[k] = complex(float64(x[2*k]), float64(x[m-1-2*k])) * t.pre[k]
	}
	t.fft.forward(t.z)
	for j := range m / 2 {
		d := t.z[j] * t.post[j]
		t.dct[2*j] = real(d)
		t.dct[m-1-2*j] = -imag(d)
	}
	return t.dct
}

// inverse writes the n outputs of the n/2 coefficients x.
func (t *imdct) inverse(x []float32, y []float32) {
	u := t.dct4(x)
	m := t.n / 2
	for i := range t.n {
		j := i + m/2
		switch {
		case j < m:
			y[i] = float32(u[j])
		case j < 2*m:
			y[i] = float32(-u[2*m-1-j])
		default:
			y[i] = float32(-u[j-2*m])
		}
	}
}

// naiveIMDCT is the definition (tests).
func naiveIMDCT(x []float32) []float32 {
	n := 2 * len(x)
	y := make([]float32, n)
	for i := range n {
		var s float64
		for k, v := range x {
			s += float64(v) * math.Cos(2*math.Pi/float64(n)*(float64(i)+0.5+float64(n)/4)*(float64(k)+0.5))
		}
		y[i] = float32(s)
	}
	return y
}

// fft is an in-place radix-2 complex FFT (forward: exp(-2πi jk/n)).
type fft struct {
	n   int
	tw  []complex128
	rev []int
}

func newFFT(n int) *fft {
	f := &fft{n: n, tw: make([]complex128, n/2), rev: make([]int, n)}
	for i := range n / 2 {
		f.tw[i] = cmplx.Exp(complex(0, -2*math.Pi*float64(i)/float64(n)))
	}
	bits := ilog(uint32(n)) - 1
	for i := range n {
		f.rev[i] = int(reverse(uint32(i), bits))
	}
	return f
}

func (f *fft) forward(a []complex128) {
	n := f.n
	for i := range n {
		if j := f.rev[i]; j > i {
			a[i], a[j] = a[j], a[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		half, step := size/2, n/size
		for s := 0; s < n; s += size {
			for k := range half {
				t := a[s+k+half] * f.tw[k*step]
				a[s+k+half] = a[s+k] - t
				a[s+k] += t
			}
		}
	}
}
