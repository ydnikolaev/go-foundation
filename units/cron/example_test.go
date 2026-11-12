package cron_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/ydnikolaev/go-foundation/units/cron"
)

// Example registers an interval task and a daily task, starts the runner and
// stops it. The interval task runs its current slot at once, so the output is
// stable.
func Example() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	runner := cron.New(logger, cron.RealClock{},
		cron.WithLocation(time.FixedZone("UTC+3", 3*3600)), // Daily is read in this zone; default UTC
	)

	ran := make(chan struct{}, 1)
	_ = runner.Add(cron.Task{
		Key:   "sweep",
		Every: 15 * time.Minute,
		Run: func(ctx context.Context, slot time.Time) error {
			ran <- struct{}{}
			return nil
		},
	})
	_ = runner.Add(cron.Task{
		Key:   "report",
		Daily: &cron.Daily{Hour: 9, Minute: 30},
		Run:   func(ctx context.Context, slot time.Time) error { return nil },
	})

	_ = runner.Start(context.Background())
	<-ran
	fmt.Println("sweep ran")
	_ = runner.Stop(context.Background())
	// Output: sweep ran
}
