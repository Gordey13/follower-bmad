package observability

import (
	"context"
	"testing"
	"time"
)

func TestSnapshotRunsDependencyChecksConcurrently(t *testing.T) {
	t.Parallel()

	service := NewHealthService(
		[]Dependency{
			{
				Name:     "postgres",
				Critical: true,
				Checker: CheckerFunc(func(ctx context.Context) error {
					time.Sleep(120 * time.Millisecond)
					return nil
				}),
			},
			{
				Name:     "minio",
				Critical: true,
				Checker: CheckerFunc(func(ctx context.Context) error {
					time.Sleep(120 * time.Millisecond)
					return nil
				}),
			},
			{
				Name:     "playwright",
				Critical: true,
				Checker: CheckerFunc(func(ctx context.Context) error {
					time.Sleep(120 * time.Millisecond)
					return nil
				}),
			},
		},
		time.Second,
		"1.0.0",
	)

	started := time.Now()
	snapshot := service.Snapshot(context.Background())
	elapsed := time.Since(started)

	if snapshot.Status != StatusReady {
		t.Fatalf("expected ready status, got %s", snapshot.Status)
	}
	if elapsed > 280*time.Millisecond {
		t.Fatalf("expected concurrent checks (<280ms), got %s", elapsed)
	}
}

func TestSnapshotUpdatesDependencyObserver(t *testing.T) {
	t.Parallel()

	service := NewHealthService(
		[]Dependency{
			{
				Name:     "postgres",
				Critical: true,
				Checker: CheckerFunc(func(ctx context.Context) error {
					return nil
				}),
			},
			{
				Name:     "minio",
				Critical: true,
				Checker: CheckerFunc(func(ctx context.Context) error {
					return context.DeadlineExceeded
				}),
			},
		},
		time.Second,
		"1.0.0",
	)

	observer := &healthDependencyObserverStub{
		values: map[string]bool{},
	}
	service.SetDependencyObserver(observer)

	snapshot := service.Snapshot(context.Background())
	if snapshot.Status != StatusNotReady {
		t.Fatalf("expected overall status %s, got %s", StatusNotReady, snapshot.Status)
	}
	if !observer.values["postgres"] {
		t.Fatalf("expected postgres readiness=true, got %+v", observer.values)
	}
	if observer.values["minio"] {
		t.Fatalf("expected minio readiness=false, got %+v", observer.values)
	}
}

type healthDependencyObserverStub struct {
	values map[string]bool
}

func (s *healthDependencyObserverStub) RecordDependencyReady(dependency string, ready bool) {
	if s.values == nil {
		s.values = map[string]bool{}
	}
	s.values[dependency] = ready
}
