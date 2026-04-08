package observability

import (
	"context"
	"sync"
	"time"
)

const (
	StatusReady    = "ready"
	StatusNotReady = "not-ready"
)

type DependencyChecker interface {
	Check(ctx context.Context) error
}

type CheckerFunc func(ctx context.Context) error

func (f CheckerFunc) Check(ctx context.Context) error {
	return f(ctx)
}

type Dependency struct {
	Name     string
	Critical bool
	Checker  DependencyChecker
}

type DependencyStatus struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Critical bool   `json:"critical"`
}

type HealthStatus struct {
	Status       string             `json:"status"`
	Dependencies []DependencyStatus `json:"dependencies"`
	Version      string             `json:"version,omitempty"`
	Diagnostics  map[string]string  `json:"diagnostics,omitempty"`
}

type HealthService struct {
	dependencies       []Dependency
	timeout            time.Duration
	version            string
	diagnostics        map[string]string
	dependencyObserver DependencyReadinessObserver
}

type DependencyReadinessObserver interface {
	RecordDependencyReady(dependency string, ready bool)
}

func NewHealthService(
	dependencies []Dependency,
	timeout time.Duration,
	version string,
	diagnostics ...map[string]string,
) *HealthService {
	safeDiagnostics := map[string]string{}
	if len(diagnostics) > 0 {
		safeDiagnostics = cloneDiagnostics(diagnostics[0])
	}

	return &HealthService{
		dependencies: dependencies,
		timeout:      timeout,
		version:      version,
		diagnostics:  safeDiagnostics,
	}
}

func (s *HealthService) SetDependencyObserver(observer DependencyReadinessObserver) {
	if s == nil {
		return
	}
	s.dependencyObserver = observer
}

func (s *HealthService) Snapshot(ctx context.Context) HealthStatus {
	overallStatus := StatusReady
	dependencies := make([]DependencyStatus, len(s.dependencies))
	results := make(chan dependencyResult, len(s.dependencies))
	observer := s.dependencyObserver

	var waitGroup sync.WaitGroup
	waitGroup.Add(len(s.dependencies))

	for index, dependency := range s.dependencies {
		go func(i int, dep Dependency) {
			defer waitGroup.Done()

			status := StatusReady
			dependencyCtx, cancel := context.WithTimeout(ctx, s.timeout)
			err := dep.Checker.Check(dependencyCtx)
			cancel()

			if err != nil {
				status = StatusNotReady
			}

			results <- dependencyResult{
				index: i,
				status: DependencyStatus{
					Name:     dep.Name,
					Status:   status,
					Critical: dep.Critical,
				},
				criticalNotReady: dep.Critical && status == StatusNotReady,
				ready:            status == StatusReady,
			}
		}(index, dependency)
	}

	waitGroup.Wait()
	close(results)

	for result := range results {
		dependencies[result.index] = result.status
		if result.criticalNotReady {
			overallStatus = StatusNotReady
		}
		if observer != nil {
			observer.RecordDependencyReady(result.status.Name, result.ready)
		}
	}

	return HealthStatus{
		Status:       overallStatus,
		Dependencies: dependencies,
		Version:      s.version,
		Diagnostics:  cloneDiagnostics(s.diagnostics),
	}
}

type dependencyResult struct {
	index            int
	status           DependencyStatus
	criticalNotReady bool
	ready            bool
}

func cloneDiagnostics(source map[string]string) map[string]string {
	if len(source) == 0 {
		return map[string]string{}
	}

	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}

	return cloned
}
