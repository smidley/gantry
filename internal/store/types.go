package store

// SeriesKey identifies one metric series. Kind is the entity class
// (host, container, disk, gpu, unraid), Entity the stable identity
// (container name, disk id, "" for host), Metric the series name
// (e.g. "cpu.total", "mem.used", "net.eth0.rx").
type SeriesKey struct {
	Kind   string
	Entity string
	Metric string
}

// Sample is one measured value. TS is unix seconds.
type Sample struct {
	TS  int64
	Val float64
}

// MetricSink is what collectors (and the fake generator) write into.
type MetricSink interface {
	Record(key SeriesKey, ts int64, val float64)
}
