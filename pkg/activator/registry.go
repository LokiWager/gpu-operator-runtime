package activator

import (
	"sort"
	"sync"

	"github.com/loki/gpu-operator-runtime/pkg/domain"
)

// Worker captures one ready runtime worker that the activator can target.
type Worker struct {
	Name                string
	Namespace           string
	ServerlessRequestID string
}

type workerEntry struct {
	worker Worker
}

// WorkerRegistry keeps the activator's ready worker registrations in memory.
type WorkerRegistry struct {
	mu      sync.Mutex
	entries map[string]*workerEntry
}

// NewWorkerRegistry builds an empty worker registry.
func NewWorkerRegistry() *WorkerRegistry {
	return &WorkerRegistry{
		entries: map[string]*workerEntry{},
	}
}

// Sync refreshes the ready worker registrations for one request ID.
func (r *WorkerRegistry) Sync(requestID string, units []domain.GPUUnitRuntime) {
	r.mu.Lock()
	defer r.mu.Unlock()

	keep := map[string]struct{}{}
	for _, unit := range units {
		if !isReadyWorkerUnit(unit, requestID) {
			continue
		}
		worker := workerFromUnit(unit)
		key := workerKey(worker.Namespace, worker.Name)
		keep[key] = struct{}{}
		entry, ok := r.entries[key]
		if !ok {
			r.entries[key] = &workerEntry{worker: worker}
			continue
		}
		entry.worker = worker
	}

	for key, entry := range r.entries {
		if entry.worker.ServerlessRequestID != requestID {
			continue
		}
		if _, ok := keep[key]; ok {
			continue
		}
		delete(r.entries, key)
	}
}

// Pick returns one registered worker for the given request ID.
func (r *WorkerRegistry) Pick(requestID string) (Worker, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	keys := make([]string, 0, len(r.entries))
	for key, entry := range r.entries {
		if entry.worker.ServerlessRequestID != requestID {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		return r.entries[key].worker, true
	}
	return Worker{}, false
}

func workerFromUnit(unit domain.GPUUnitRuntime) Worker {
	return Worker{
		Name:                unit.Name,
		Namespace:           unit.Namespace,
		ServerlessRequestID: unit.Serverless.RequestID,
	}
}

func workerKey(namespace, name string) string {
	return namespace + "/" + name
}
