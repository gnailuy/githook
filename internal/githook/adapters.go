package githook

import (
	"context"
	"fmt"
	"regexp"
	"time"
)

var identityPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var envPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
var servicePattern = regexp.MustCompile(`^[a-zA-Z0-9_.@-]+\.service$`)

type TargetAdapter interface {
	Process(context.Context, Job) error
}

type AdapterRegistry struct {
	adapters map[string]TargetAdapter
	sources  map[string]string
}

func NewAdapterRegistry() *AdapterRegistry {
	return &AdapterRegistry{adapters: make(map[string]TargetAdapter), sources: make(map[string]string)}
}

func (r *AdapterRegistry) RegisterTarget(id string, adapter TargetAdapter) error {
	if !identityPattern.MatchString(id) || adapter == nil {
		return fmt.Errorf("invalid target registration")
	}
	if _, exists := r.adapters[id]; exists {
		return fmt.Errorf("target %q already registered", id)
	}
	r.adapters[id] = adapter
	return nil
}

func (r *AdapterRegistry) RegisterSource(sourceID, targetID string) error {
	if !identityPattern.MatchString(sourceID) || !identityPattern.MatchString(targetID) {
		return fmt.Errorf("invalid source registration")
	}
	if _, exists := r.adapters[targetID]; !exists {
		return fmt.Errorf("target %q is not registered", targetID)
	}
	if _, exists := r.sources[sourceID]; exists {
		return fmt.Errorf("source %q already registered", sourceID)
	}
	r.sources[sourceID] = targetID
	return nil
}

func (r *AdapterRegistry) Process(ctx context.Context, job Job) error {
	targetID, ok := r.sources[job.SourceID]
	if !ok || targetID != job.TargetID {
		return permanent(fmt.Errorf("source and target are not registered together"))
	}
	adapter := r.adapters[targetID]
	if err := adapter.Process(ctx, job); err != nil {
		return err
	}
	return nil
}

type MultiWorker struct {
	Queue    *Queue
	Registry *AdapterRegistry
}

func (w MultiWorker) Process(ctx context.Context, job Job) error {
	if w.Queue == nil || w.Registry == nil {
		return fmt.Errorf("queue and adapter registry are required")
	}
	stateID := job.TargetID + ":" + job.SourceID
	older, err := w.Queue.RefuseOlderFor(ctx, stateID, job.RunID)
	if err != nil {
		return err
	}
	if older {
		return permanent(fmt.Errorf("run is older than or equal to target deployment state"))
	}
	if err := w.Registry.Process(ctx, job); err != nil {
		return err
	}
	return w.Queue.MarkDeployedFor(ctx, stateID, job.RunID, job.HeadSHA)
}

func (w MultiWorker) Run(ctx context.Context) error {
	if err := w.Queue.Recover(ctx); err != nil {
		return err
	}
	for {
		job, err := w.Queue.Claim(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(5 * time.Second):
				continue
			}
		}
		if err = w.Process(ctx, job); err != nil {
			if isPermanent(err) || job.Attempts > MaxTransientRetries {
				err = w.Queue.Fail(ctx, job.DeliveryID, err.Error())
			} else {
				err = w.Queue.Retry(ctx, job.DeliveryID, err.Error(), retryDelay(job.Attempts))
			}
			if err != nil {
				return err
			}
			continue
		}
		if err := w.Queue.Complete(ctx, job.DeliveryID); err != nil {
			return err
		}
	}
}
