package dev

import "sync"

type GlobalState struct {
	Paused      bool
	cancelQueue []func()
	mu          sync.Mutex
}

func (g *GlobalState) AddCancelFunc(cancel func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cancelQueue = append(g.cancelQueue, cancel)
}

func (g *GlobalState) Cancel() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, cancel := range g.cancelQueue {
		cancel()
	}
	g.cancelQueue = nil
}
