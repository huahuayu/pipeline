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
	Process(ctx context.Context, job any) error
	Connect(next INode)
	Next() []INode
	Wait()
	Metrics() *NodeMetrics
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
	BufferSize     int
	Workers        int
	MaxRetries     int
	RetryDelay     time.Duration
	CircuitBreaker CircuitBreakerConfig
}

// CircuitBreakerConfig configures the circuit breaker
type CircuitBreakerConfig struct {
	Enabled          bool
	FailureThreshold int
	ResetTimeout     time.Duration
}

// DefaultConfig returns a sensible default configuration
func DefaultConfig() NodeConfig {
	numCPU := runtime.NumCPU()
	return NodeConfig{
		BufferSize: numCPU * 4,
		Workers:    numCPU,
		MaxRetries: 0, // No retries by default
		RetryDelay: time.Millisecond * 100,
		CircuitBreaker: CircuitBreakerConfig{
			Enabled:          false, // Disabled by default
			FailureThreshold: 5,
			ResetTimeout:     30 * time.Second,
		},
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

	// Circuit breaker
	breaker *CircuitBreaker
}

// Job represents a unit of work
type Job[T any] struct {
	ctx    context.Context
	data   T
	result chan Result
}

// Result represents the outcome of processing
type Result struct {
	Data any
	Err  error
}

// CircuitBreaker implements circuit breaker pattern
type CircuitBreaker struct {
	state           atomic.Int32
	failures        atomic.Int32
	lastFailureTime atomic.Int64
	config          CircuitBreakerConfig
}

const (
	circuitClosed int32 = iota
	circuitOpen
	circuitHalfOpen
)

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
		onError:   func(err error) { /* Default: no-op. Users should set custom error handler */ },
	}

	if cfg.CircuitBreaker.Enabled {
		n.breaker = &CircuitBreaker{
			config: cfg.CircuitBreaker,
		}
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

	// Start worker goroutines
	for i := 0; i < n.config.Workers; i++ {
		n.workers.Add(1)
		go n.work(i)
	}

	// Node started with n.config.Workers workers
	return nil
}

// Process submits a job for processing
func (n *Node[In, Out]) Process(ctx context.Context, jobData any) error {
	// Check circuit breaker
	if n.breaker != nil && !n.breaker.Allow() {
		return fmt.Errorf("circuit breaker open for node %s", n.name)
	}

	// Type assertion
	data, ok := jobData.(In)
	if !ok {
		return fmt.Errorf("invalid input type for node %s: expected %T, got %T",
			n.name, *new(In), jobData)
	}

	// Create job with result channel
	job := &Job[In]{
		ctx:    ctx,
		data:   data,
		result: make(chan Result, 1),
	}

	// Submit job with timeout
	select {
	case n.jobQueue <- job:
		n.metrics.CurrentQueueSize.Add(1)

		// Wait for result
		select {
		case result := <-job.result:
			if result.Err != nil {
				return result.Err
			}

			// Forward to downstream nodes
			if result.Data != nil && len(n.next) > 0 {
				return n.forward(ctx, result.Data)
			}

			return nil

		case <-ctx.Done():
			return ctx.Err()
		case <-n.ctx.Done():
			return ErrPipelineStopped
		}

	case <-ctx.Done():
		return ctx.Err()
	case <-n.ctx.Done():
		return ErrPipelineStopped
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timeout submitting job to node %s", n.name)
	}
}

// work is the main worker loop
func (n *Node[In, Out]) work(id int) {
	defer n.workers.Done()
	defer func() {
		if r := recover(); r != nil {
			n.onError(fmt.Errorf("worker %d panic: %v", id, r))
			// Don't restart worker - let the pipeline handle worker pool size
		}
	}()

	for {
		select {
		case <-n.ctx.Done():
			// Worker shutting down
			return

		case job := <-n.jobQueue:
			n.metrics.CurrentQueueSize.Add(-1)
			n.processJob(job)
		}
	}
}

// processJob handles a single job with retries
func (n *Node[In, Out]) processJob(job *Job[In]) {
	start := time.Now()

	var result Out
	var err error

	// Retry logic
	for attempt := 0; attempt <= n.config.MaxRetries; attempt++ {
		// Check if job context is still valid
		if job.ctx.Err() != nil {
			job.result <- Result{Err: job.ctx.Err()}
			return
		}

		// Apply exponential backoff for retries
		if attempt > 0 {
			delay := n.config.RetryDelay * time.Duration(1<<(attempt-1))
			select {
			case <-time.After(delay):
			case <-job.ctx.Done():
				job.result <- Result{Err: job.ctx.Err()}
				return
			case <-n.ctx.Done():
				job.result <- Result{Err: ErrPipelineStopped}
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

			result, processingErr = n.processor(job.ctx, job.data)
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
		if n.breaker != nil {
			n.breaker.RecordFailure()
		}
		n.onError(fmt.Errorf("processing failed after %d attempts: %w",
			n.config.MaxRetries+1, err))
		job.result <- Result{Err: err}
	} else {
		n.metrics.ProcessedCount.Add(1)
		if n.breaker != nil {
			n.breaker.RecordSuccess()
		}
		job.result <- Result{Data: result, Err: nil}
	}
}

// forward sends data to all downstream nodes
func (n *Node[In, Out]) forward(ctx context.Context, data any) error {
	n.mu.RLock()
	downstream := make([]INode, len(n.next))
	copy(downstream, n.next)
	n.mu.RUnlock()

	if len(downstream) == 0 {
		return nil
	}

	// Process downstream nodes concurrently
	var wg sync.WaitGroup
	errCh := make(chan error, len(downstream))

	for _, node := range downstream {
		wg.Add(1)
		go func(next INode) {
			defer wg.Done()
			if err := next.Process(ctx, data); err != nil {
				errCh <- fmt.Errorf("forward to %s failed: %w", next.Name(), err)
			}
		}(node)
	}

	wg.Wait()
	close(errCh)

	// Collect errors
	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// Connect adds a downstream node
func (n *Node[In, Out]) Connect(next INode) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.next = append(n.next, next)
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
	if n.cancel != nil {
		n.cancel()
	}
	n.workers.Wait()
}

// Metrics returns the node's metrics
func (n *Node[In, Out]) Metrics() *NodeMetrics {
	return &n.metrics
}

// Allow checks if a request should be allowed
func (cb *CircuitBreaker) Allow() bool {
	state := cb.state.Load()

	switch state {
	case circuitClosed:
		return true

	case circuitOpen:
		// Check if we should transition to half-open
		lastFailure := time.Unix(0, cb.lastFailureTime.Load())
		if time.Since(lastFailure) > cb.config.ResetTimeout {
			if cb.state.CompareAndSwap(circuitOpen, circuitHalfOpen) {
				cb.failures.Store(0)
				// Circuit breaker transitioning to half-open
			}
			return true
		}
		return false

	case circuitHalfOpen:
		return true

	default:
		return true
	}
}

// RecordFailure records a failure
func (cb *CircuitBreaker) RecordFailure() {
	failures := cb.failures.Add(1)
	cb.lastFailureTime.Store(time.Now().UnixNano())

	if failures >= int32(cb.config.FailureThreshold) {
		if cb.state.CompareAndSwap(circuitHalfOpen, circuitOpen) ||
			cb.state.CompareAndSwap(circuitClosed, circuitOpen) {
			// Circuit breaker opened after failures threshold
		}
	}
}

// RecordSuccess records a success
func (cb *CircuitBreaker) RecordSuccess() {
	if cb.state.CompareAndSwap(circuitHalfOpen, circuitClosed) {
		cb.failures.Store(0)
		// Circuit breaker closed after successful request
	}
}
