package tui

import (
	"errors"
	"sync"
	"testing"

	"sidekick/client"

	"github.com/stretchr/testify/assert"
)

// Published tasks and their referenced data must remain immutable.
type pollingClient struct {
	mockClient
	mu       sync.RWMutex
	response getTaskResponse
}

func (c *pollingClient) publish(task client.Task, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.response = getTaskResponse{task: task, err: err}
}

func (c *pollingClient) GetTask(workspaceID, taskID string) (client.Task, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.response.task, c.response.err
}

func TestPollingClientConcurrentTransitions(t *testing.T) {
	t.Parallel()
	first := newTestTask()
	second := newTestTaskWithFlows()
	responseError := errors.New("polling unavailable")
	c := &pollingClient{}
	c.publish(first, nil)
	publish := c.publish

	start := make(chan struct{})
	var wg sync.WaitGroup
	for reader := 0; reader < 4; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Errorf("polling panicked during response transition: %v", recovered)
				}
			}()
			<-start
			for i := 0; i < 2000; i++ {
				task, err := c.GetTask("workspace1", "task1")
				if err == nil {
					assert.Equal(t, first, task)
				} else {
					assert.ErrorIs(t, err, responseError)
					assert.Equal(t, second, task)
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 2000; i++ {
			publish(second, responseError)
			publish(first, nil)
		}
	}()
	close(start)
	wg.Wait()

	publish(second, responseError)
	for i := 0; i < 10; i++ {
		task, err := c.GetTask("workspace1", "task1")
		assert.Equal(t, second, task)
		assert.ErrorIs(t, err, responseError)
	}
}
