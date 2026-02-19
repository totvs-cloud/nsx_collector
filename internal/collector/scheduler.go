package collector

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// Scheduler runs all workers on a fixed interval.
type Scheduler struct {
	workers  []*Worker
	interval time.Duration
	logger   *zap.Logger
}

// NewScheduler creates a scheduler that collects from all workers every interval.
func NewScheduler(workers []*Worker, interval time.Duration) *Scheduler {
	return &Scheduler{
		workers:  workers,
		interval: interval,
		logger:   zap.L().Named("scheduler"),
	}
}

// Start begins the collection loop and blocks until the context is cancelled.
func (s *Scheduler) Start(ctx context.Context) error {
	s.logger.Info("scheduler starting",
		zap.Int("managers", len(s.workers)),
		zap.Duration("interval", s.interval),
	)

	// Run one cycle immediately on startup
	s.runAll(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("scheduler shutting down")
			return nil
		case <-ticker.C:
			s.runAll(ctx)
		}
	}
}

// runAll runs all workers concurrently.
func (s *Scheduler) runAll(ctx context.Context) {
	done := make(chan struct{}, len(s.workers))
	for _, w := range s.workers {
		go func(worker *Worker) {
			defer func() { done <- struct{}{} }()
			worker.Collect(ctx)
		}(w)
	}
	for range s.workers {
		select {
		case <-done:
		case <-ctx.Done():
			return
		}
	}
}
