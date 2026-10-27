package recurring

import (
	"context"
	"log"
	"time"

	"gorm.io/gorm"

	"beprod/internal/jakarta"
)

// NextRun returns today 00:05 WIB if now is before it, else tomorrow 00:05 WIB.
func NextRun(now time.Time) time.Time {
	n := now.In(jakarta.Loc)
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 5, 0, 0, jakarta.Loc)
	if n.Before(today) {
		return today
	}
	return today.AddDate(0, 0, 1)
}

// RunScheduler: GenerateAll once at start, then wait until NextRun(jakarta.Now()), run, repeat; returns when ctx done.
func RunScheduler(ctx context.Context, db *gorm.DB) {
	run := func() {
		if err := GenerateAll(db); err != nil {
			log.Printf("recurring: scheduler: %v", err)
		}
	}
	run()
	for {
		now := jakarta.Now()
		timer := time.NewTimer(NextRun(now).Sub(now))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			run()
		}
	}
}
