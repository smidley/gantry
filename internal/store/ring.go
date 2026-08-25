package store

// Ring is a fixed-capacity FIFO of samples. Not goroutine-safe;
// callers synchronize (Live wraps rings in a lock).
type Ring struct {
	buf  []Sample
	head int // index of oldest
	n    int
}

func NewRing(capacity int) *Ring {
	return &Ring{buf: make([]Sample, capacity)}
}

func (r *Ring) Append(s Sample) {
	if r.n < len(r.buf) {
		r.buf[(r.head+r.n)%len(r.buf)] = s
		r.n++
		return
	}
	r.buf[r.head] = s
	r.head = (r.head + 1) % len(r.buf)
}

func (r *Ring) Len() int { return r.n }

func (r *Ring) Latest() (Sample, bool) {
	if r.n == 0 {
		return Sample{}, false
	}
	return r.buf[(r.head+r.n-1)%len(r.buf)], true
}

// Since returns samples with TS >= ts, oldest first.
func (r *Ring) Since(ts int64) []Sample {
	out := make([]Sample, 0, r.n)
	for i := 0; i < r.n; i++ {
		s := r.buf[(r.head+i)%len(r.buf)]
		if s.TS >= ts {
			out = append(out, s)
		}
	}
	return out
}
