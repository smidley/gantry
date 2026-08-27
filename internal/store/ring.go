package store

// Ring is a fixed-capacity FIFO of samples. Not goroutine-safe;
// callers synchronize (Live wraps rings in a lock).
type Ring struct {
	buf  []Sample
	head int // index of oldest
	n    int
}

func NewRing(capacity int) *Ring {
	if capacity < 1 {
		capacity = 1
	}
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

// AppendSince appends every sample with TS >= ts, oldest first, onto dst
// and returns the resulting slice, the same way append() does. A caller
// that walks many rings for the same ts (FlushMinutes, catching up
// several windows across every series) can pass the same backing slice,
// resliced to length 0, back in on every call, reusing one growing
// buffer instead of paying for a fresh ring-capacity allocation per ring.
func (r *Ring) AppendSince(ts int64, dst []Sample) []Sample {
	for i := 0; i < r.n; i++ {
		s := r.buf[(r.head+i)%len(r.buf)]
		if s.TS >= ts {
			dst = append(dst, s)
		}
	}
	return dst
}

// Since returns samples with TS >= ts, oldest first.
func (r *Ring) Since(ts int64) []Sample {
	return r.AppendSince(ts, nil)
}
