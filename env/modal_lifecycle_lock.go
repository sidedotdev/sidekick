package env

import (
	"context"
	"sync"
)

// Activity inputs deserialize into separate Env instances, so lifecycle
// coordination must be shared across the worker process.
var modalLifecycleLocks = struct {
	sync.Mutex
	entries map[string]*modalLifecycleLock
}{
	entries: make(map[string]*modalLifecycleLock),
}

type modalLifecycleLock struct {
	token chan struct{}
	users int
}

func acquireModalLifecycleLock(ctx context.Context, name string) (func(), error) {
	modalLifecycleLocks.Lock()
	entry := modalLifecycleLocks.entries[name]
	if entry == nil {
		entry = &modalLifecycleLock{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		modalLifecycleLocks.entries[name] = entry
	}
	entry.users++
	modalLifecycleLocks.Unlock()

	dropUser := func() {
		modalLifecycleLocks.Lock()
		defer modalLifecycleLocks.Unlock()
		entry.users--
		if entry.users == 0 {
			delete(modalLifecycleLocks.entries, name)
		}
	}

	select {
	case <-ctx.Done():
		dropUser()
		return nil, ctx.Err()
	case <-entry.token:
	}

	release := func() {
		entry.token <- struct{}{}
		dropUser()
	}
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}
