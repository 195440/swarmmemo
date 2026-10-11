package rubric

import (
	"encoding/binary"
	"math"
	"math/bits"
)

// rng is xoshiro256** (Blackman and Vigna), specified here so another
// implementation can reproduce every draw.
type rng struct{ s [4]uint64 }

func newRNG(seed [32]byte) *rng {
	r := &rng{}
	for i := range r.s {
		r.s[i] = binary.BigEndian.Uint64(seed[8*i:])
	}
	if r.s == [4]uint64{} {
		r.s[0] = 1
	}
	return r
}

func (r *rng) next() uint64 {
	s := &r.s
	result := bits.RotateLeft64(s[1]*5, 7) * 9
	t := s[1] << 17
	s[2] ^= s[0]
	s[3] ^= s[1]
	s[1] ^= s[2]
	s[0] ^= s[3]
	s[2] ^= t
	s[3] = bits.RotateLeft64(s[3], 45)
	return result
}

// uniform is in [0, 1).
func (r *rng) uniform() float64 { return float64(r.next()>>11) * (1.0 / (1 << 53)) }

// normal is Box-Muller, cos branch: two uniforms per normal.
func (r *rng) normal() float64 {
	u1 := 1 - r.uniform() // (0, 1]
	u2 := r.uniform()
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}

// gamma is Marsaglia-Tsang for shape a ≥ 1; below 1, Gamma(a+1)·U^(1/a).
func (r *rng) gamma(a float64) float64 {
	if a < 1 {
		u := 1 - r.uniform()
		return r.gamma(a+1) * math.Pow(u, 1/a)
	}
	d := a - 1.0/3
	c := 1 / math.Sqrt(9*d)
	for {
		x := r.normal()
		v := float64(1 + c*x)
		if v <= 0 {
			continue
		}
		v = float64(v * v * v)
		u := r.uniform()
		x2 := float64(x * x)
		if u < 1-0.0331*float64(x2*x2) {
			return d * v
		}
		if u > 0 && math.Log(u) < 0.5*x2+d*(1-v+math.Log(v)) {
			return d * v
		}
	}
}

// beta is X/(X+Y), X ~ Gamma(a), Y ~ Gamma(b).
func (r *rng) beta(a, b float64) float64 {
	x := r.gamma(a)
	y := r.gamma(b)
	if x+y == 0 {
		return 0.5
	}
	return x / (x + y)
}
