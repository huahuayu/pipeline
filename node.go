package pipeline

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// INode defines the interface for pipeline nodes
type INode interface {
	Name() string
	Start(ctx context.Context) error
	Process(job any) error
	Connect(next INode)
	Next() []INode
	Wait()
	Metrics() *NodeMetrics
	AddUpstreamSource()
	MarkInputClosed()
}

// NodeMetrics contains runtime metrics for a node
type NodeMetrics struct {
	ProcessedCount   atomic.Int64
	FailedCount      atomic.Int64
	TotalLatency     atomic.Int64 // Total latency in nanoseconds
	CurrentQueueSize atomic.Int32
}

// GetAverageLatency returns the average processing latency
func (m *NodeMetrics) GetAverageLatency() time.Duration {
	processed := m.ProcessedCount.Load()
	if processed == 0 {
		return 0
	}
	return time.Duration(m.TotalLatency.Load() / processed)
}

// NodeConfig contains configuration for a node
type NodeConfig struct {
	BufferSize int
	Workers    int
	MaxRetries int
	RetryDelay time.Duration
}

// DefaultConfig returns a sensible default configuration
func DefaultConfig() NodeConfig {
	numCPU := runtime.NumCPU()
	return NodeConfig{
		BufferSize: numCPU * 4,
		Workers:    numCPU,
		MaxRetries: 0,
		RetryDelay: 100 * time.Millisecond,
	}
}

// Node represents a processing node in the pipeline
type Node[In, Out any] struct {
	name      string
	config    NodeConfig
	processor func(context.Context, In) (Out, error)

	// Channels
	jobQueue chan *Job[In]

	// Downstream nodes
	mu   sync.RWMutex
	next []INode

	// Lifecycle
	ctx     context.Context
	cancel  context.CancelFunc
	workers sync.WaitGroup

	// Metrics
	metrics NodeMetrics

	// Error handling
	onError func(error)

	// Object pool
	jobPool sync.Pool

	// Upstream tracking
	upstreamSources atomic.Int32
}

// Job represents a unit of work
type Job[T any] struct {
	data T
}

// NewNode creates a new processing node
func NewNode[In, Out any](
	name string,
	processor func(context.Context, In) (Out, error),
	config ...NodeConfig,
) *Node[In, Out] {
	if processor == nil {
		panic("processor function cannot be nil")
	}

	cfg := DefaultConfig()
	if len(config) > 0 {
		cfg = config[0]
	}

	n := &Node[In, Out]{
		name:      name,
		config:    cfg,
		processor: processor,
		jobQueue:  make(chan *Job[In], cfg.BufferSize),
		next:      make([]INode, 0),
		onError:   func(err error) {},
	}

	n.jobPool = sync.Pool{
		New: func() any {
			job := &Job[In]{}
			return job
		},
	}

	return n
}

// WithErrorHandler sets a custom error handler
func (n *Node[In, Out]) WithErrorHandler(handler func(error)) *Node[In, Out] {
	n.onError = handler
	return n
}

// Name returns the node's name
func (n *Node[In, Out]) Name() string {
	return n.name
}

// Start initializes the node and starts its workers
func (n *Node[In, Out]) Start(ctx context.Context) error {
	n.ctx, n.cancel = context.WithCancel(ctx)

	for i := 0; i < n.config.Workers; i++ {
		n.workers.Add(1)
		go n.work()
	}

	return nil
}

// Process submits a job for processing
func (n *Node[In, Out]) Process(jobData any) error {
	// Type assertion
	data, ok := jobData.(In)
	if !ok {
		return fmt.Errorf("invalid input type for node %s: expected %T, got %T",
			n.name, *new(In), jobData)
	}

	// Get job from pool
	job := n.jobPool.Get().(*Job[In])
	job.data = data

	// Submit job to queue
	if err := n.submit(job); err != nil {
		// If submission fails, we must return the job to pool immediately
		n.releaseJob(job)
		return err
	}

	return nil
}

// releaseJob resets and returns a job to the pool
func (n *Node[In, Out]) releaseJob(job *Job[In]) {
	var zero In
	job.data = zero // Avoid memory leaks
	n.jobPool.Put(job)
}

// submit adds a job to the queue
func (n *Node[In, Out]) submit(job *Job[In]) error {
	select {
	case n.jobQueue <- job:
		n.metrics.CurrentQueueSize.Add(1)
		return nil
	case <-n.ctx.Done():
		return ErrPipelineStopped
	}
}

// work is the main worker loop
func (n *Node[In, Out]) work() {
	defer n.workers.Done()
	defer func() {
		if r := recover(); r != nil {
			n.onError(fmt.Errorf("worker panic: %v", r))
		}
	}()

	for {
		select {
		case <-n.ctx.Done():
			return
		case job, ok := <-n.jobQueue:
			if !ok {
				// Queue closed, propagate to downstream
				n.propagateClose()
				return
			}
			n.metrics.CurrentQueueSize.Add(-1)
			n.processJob(job)
		}
	}
}

// propagateClose notifies all downstream nodes that input is closed
func (n *Node[In, Out]) propagateClose() {
	n.mu.RLock()
	defer n.mu.RUnlock()
	for _, next := range n.next {
		next.MarkInputClosed()
	}
}

// processJob handles a single job with retries
func (n *Node[In, Out]) processJob(job *Job[In]) {
	start := time.Now()

	var result Out
	var err error

	for attempt := 0; attempt <= n.config.MaxRetries; attempt++ {
		// Check if pipeline is stopping
		if n.ctx.Err() != nil {
			n.releaseJob(job)
			return
		}

		// Exponential backoff for retries
		if attempt > 0 {
			delay := n.config.RetryDelay * time.Duration(1<<(attempt-1))
			select {
			case <-time.After(delay):
			case <-n.ctx.Done():
				n.releaseJob(job)
				return
			}
		}

		// Execute processor with panic recovery
		err = func() (processingErr error) {
			defer func() {
				if r := recover(); r != nil {
					processingErr = fmt.Errorf("panic: %v", r)
				}
			}()
			result, processingErr = n.processor(n.ctx, job.data)
			return processingErr
		}()

		if err == nil {
			break
		}
	}

	// Update metrics
	elapsed := time.Since(start)
	n.metrics.TotalLatency.Add(elapsed.Nanoseconds())

	if err != nil {
		n.metrics.FailedCount.Add(1)
		n.onError(fmt.Errorf("processing failed after %d attempts: %w",
			n.config.MaxRetries+1, err))
		n.releaseJob(job)
	} else {
		n.metrics.ProcessedCount.Add(1)
		if fErr := n.forward(result); fErr != nil {
			n.onError(fmt.Errorf("failed to forward result: %w", fErr))
		}
		// In Async mode, we are done with the job here
		n.releaseJob(job)
	}
}

// forward sends data to all downstream nodes
func (n *Node[In, Out]) forward(data any) error {
	n.mu.RLock()
	count := len(n.next)
	if count == 0 {
		n.mu.RUnlock()
		return nil
	}

	// Fast path: single downstream node
	if count == 1 {
		next := n.next[0]
		n.mu.RUnlock()
		return next.Process(data)
	}

	// Copy for concurrent access
	downstream := make([]INode, count)
	copy(downstream, n.next)
	n.mu.RUnlock()

	// Fan-out to multiple downstream nodes
	errCh := make(chan error, count)
	var wg sync.WaitGroup
	wg.Add(count)

	for _, next := range downstream {
		go func(node INode) {
			defer wg.Done()
			if err := node.Process(data); err != nil {
				errCh <- err
			}
		}(next)
	}

	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Connect adds a downstream node
func (n *Node[In, Out]) Connect(next INode) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.next = append(n.next, next)
	next.AddUpstreamSource()
}

// AddUpstreamSource increments the upstream source count
func (n *Node[In, Out]) AddUpstreamSource() {
	n.upstreamSources.Add(1)
}

// MarkInputClosed decrements the upstream source count and closes the queue if zero
func (n *Node[In, Out]) MarkInputClosed() {
	if n.upstreamSources.Add(-1) == 0 {
		close(n.jobQueue)
	}
}

// Next returns all downstream nodes
func (n *Node[In, Out]) Next() []INode {
	n.mu.RLock()
	defer n.mu.RUnlock()

	result := make([]INode, len(n.next))
	copy(result, n.next)
	return result
}

// Wait blocks until all workers have finished
func (n *Node[In, Out]) Wait() {
	n.workers.Wait()
}

// Metrics returns the node's metrics
func (n *Node[In, Out]) Metrics() *NodeMetrics {
	return &n.metrics
}
