package store

import (
	"context"
	"time"
)

func (s *Store) Maintain(ctx context.Context, now time.Time, ret Retention) error {
	if _, err := s.FlushMinutes(ctx, now); err != nil {
		return err
	}
	if err := s.DownsampleOnce(ctx, now); err != nil {
		return err
	}
	return s.PruneOnce(ctx, now, ret)
}

func RetentionFromConfig(get func(key string, def int) int) Retention {
	d := DefaultRetention()
	return Retention{
		R1:           time.Duration(get("retention.r1_hours", int(d.R1/time.Hour))) * time.Hour,
		R2:           time.Duration(get("retention.r2_days", int(d.R2/(24*time.Hour)))) * 24 * time.Hour,
		R3:           time.Duration(get("retention.r3_days", int(d.R3/(24*time.Hour)))) * 24 * time.Hour,
		SizeCapBytes: int64(get("retention.size_cap_mb", int(d.SizeCapBytes>>20))) << 20,
	}
}
