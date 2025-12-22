package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// ErrPipelineStopped indicates the pipeline has been stopped
var ErrPipelineStopped = errors.New("pipeline stopped")

// Pipeline represents a high-performance data processing pipeline
type Pipeline struct {
	root      INode
	ctx       context.Context
	cancel    context.CancelFunc
	state     atomic.Int32
	startOnce sync.Once
	stopOnce  sync.Once
}

const (
	stateCreated int32 = iota
	stateRunning
	stateStopping
	stateStopped
)

// NewPipeline creates a new pipeline with the given root node
func NewPipeline(root INode) (*Pipeline, error) {
	if root == nil {
		return nil, errors.New("root node cannot be nil")
	}

	p := &Pipeline{root: root}

	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("pipeline validation failed: %w", err)
	}

	return p, nil
}

// validate checks the pipeline for structural issues like cycles
func (p *Pipeline) validate() error {
	if p.hasCycle() {
		return errors.New("pipeline contains cycle")
	}
	return nil
}

// Start initializes and starts the pipeline
func (p *Pipeline) Start(ctx context.Context) error {
	var startErr error

	p.startOnce.Do(func() {
		if !p.state.CompareAndSwap(stateCreated, stateRunning) {
			startErr = errors.New("pipeline already started or stopped")
			return
		}

		p.ctx, p.cancel = context.WithCancel(ctx)

		// Register pipeline as upstream source for root
		p.root.AddUpstreamSource()

		// Start all nodes
		p.traverseNodes(func(node INode) {
			if startErr != nil {
				return
			}
			if err := node.Start(p.ctx); err != nil {
				startErr = fmt.Errorf("failed to start node %s: %w", node.Name(), err)
			}
		})

		if startErr != nil {
			p.state.Store(stateCreated)
			if p.cancel != nil {
				p.cancel()
			}
		}
	})

	return startErr
}

// Stop gracefully shuts down the pipeline
func (p *Pipeline) Stop(timeout time.Duration) error {
	var stopErr error

	p.stopOnce.Do(func() {
		if !p.state.CompareAndSwap(stateRunning, stateStopping) {
			stopErr = errors.New("pipeline not running")
			return
		}

		// Signal root node to stop accepting new jobs and drain queue
		p.root.MarkInputClosed()

		// Wait for graceful shutdown with timeout
		done := make(chan struct{})
		go func() {
			p.traverseNodes(func(node INode) {
				node.Wait()
			})
			close(done)
		}()

		if timeout > 0 {
			select {
			case <-done:
				// Graceful shutdown completed
			case <-time.After(timeout):
				stopErr = errors.New("shutdown timeout exceeded")
			}
		} else {
			// Wait indefinitely
			<-done
		}

		// Always cancel context to ensure resources are released
		if p.cancel != nil {
			p.cancel()
		}

		p.state.Store(stateStopped)
	})

	return stopErr
}

// Send sends a job through the pipeline
func (p *Pipeline) Send(job any) error {
	if p.state.Load() != stateRunning {
		return ErrPipelineStopped
	}

	return p.root.Process(job)
}

// IsRunning returns true if the pipeline is currently running
func (p *Pipeline) IsRunning() bool {
	return p.state.Load() == stateRunning
}

// traverseNodes applies a function to all nodes in the pipeline
func (p *Pipeline) traverseNodes(fn func(INode)) {
	visited := make(map[INode]bool)
	p.traverseHelper(p.root, visited, fn)
}

func (p *Pipeline) traverseHelper(node INode, visited map[INode]bool, fn func(INode)) {
	if visited[node] {
		return
	}
	visited[node] = true
	fn(node)

	for _, next := range node.Next() {
		p.traverseHelper(next, visited, fn)
	}
}

// hasCycle detects cycles in the pipeline graph
func (p *Pipeline) hasCycle() bool {
	visited := make(map[INode]bool)
	recStack := make(map[INode]bool)

	var detectCycle func(INode) bool
	detectCycle = func(node INode) bool {
		visited[node] = true
		recStack[node] = true

		for _, next := range node.Next() {
			if !visited[next] {
				if detectCycle(next) {
					return true
				}
			} else if recStack[next] {
				return true
			}
		}

		recStack[node] = false
		return false
	}

	return detectCycle(p.root)
}
