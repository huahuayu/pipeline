package pipeline

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestPipelineStress tests the pipeline under extreme load
func TestPipelineStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	// Create a pipeline with multiple nodes
	config := DefaultConfig()
	config.Workers = 16
	config.BufferSize = 1000

	processed := atomic.Int64{}

	node1 := NewNode[int, int]("node1",
		func(ctx context.Context, i int) (int, error) {
			return i * 2, nil
		}, config)

	node2 := NewNode[int, int]("node2",
		func(ctx context.Context, i int) (int, error) {
			return i + 10, nil
		}, config)

	node3 := NewNode[int, int]("node3",
		func(ctx context.Context, i int) (int, error) {
			processed.Add(1)
			return i, nil
		}, config)

	// Create branching pipeline
	node1.Connect(node2)
	node1.Connect(node3)
	node2.Connect(node3)

	p, err := NewPipeline(node1)
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}

	ctx := context.Background()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Failed to start pipeline: %v", err)
	}

	// Send a massive number of jobs concurrently
	const numJobs = 10000
	const numSenders = 100

	start := time.Now()
	var wg sync.WaitGroup
	errors := atomic.Int32{}

	for s := 0; s < numSenders; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < numJobs/numSenders; i++ {
				if err := p.SendWithTimeout(i, 5*time.Second); err != nil {
					errors.Add(1)
				}
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	// Give time for processing to complete
	time.Sleep(100 * time.Millisecond)

	// Stop pipeline
	if err := p.Stop(10 * time.Second); err != nil {
		t.Errorf("Failed to stop pipeline: %v", err)
	}

	// Verify results
	if errors.Load() > 0 {
		t.Errorf("Had %d errors during stress test", errors.Load())
	}

	// Each job goes through 2 paths, so we expect more processed than sent
	t.Logf("Stress test: Sent %d jobs, processed %d jobs in %v (%.0f jobs/sec)",
		numJobs, processed.Load(), elapsed, float64(numJobs)/elapsed.Seconds())

	if processed.Load() < int64(numJobs) {
		t.Errorf("Expected at least %d processed jobs, got %d", numJobs, processed.Load())
	}
}

// TestPipelineMemoryLeak tests for memory leaks
func TestPipelineMemoryLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping memory leak test in short mode")
	}

	// Create and destroy pipelines repeatedly
	for i := 0; i < 100; i++ {
		node := NewNode[int, int]("test",
			func(ctx context.Context, i int) (int, error) {
				return i * 2, nil
			})

		p, err := NewPipeline(node)
		if err != nil {
			t.Fatalf("Failed to create pipeline: %v", err)
		}

		ctx := context.Background()
		if err := p.Start(ctx); err != nil {
			t.Fatalf("Failed to start pipeline: %v", err)
		}

		// Send some jobs
		for j := 0; j < 10; j++ {
			p.SendWithTimeout(j, time.Second)
		}

		// Stop and ensure cleanup
		if err := p.Stop(time.Second); err != nil {
			t.Errorf("Failed to stop pipeline: %v", err)
		}
	}

	// If we get here without running out of memory, test passes
	t.Log("Memory leak test completed successfully")
}

// TestPipelineEdgeCases tests various edge cases
func TestPipelineEdgeCases(t *testing.T) {
	t.Run("SendBeforeStart", func(t *testing.T) {
		node := NewNode[string, string]("test",
			func(ctx context.Context, s string) (string, error) {
				return s, nil
			})

		p, _ := NewPipeline(node)

		// Try to send before starting
		err := p.SendWithTimeout("test", time.Second)
		if err == nil {
			t.Error("Expected error when sending before start")
		}
	})

	t.Run("DoubleStart", func(t *testing.T) {
		node := NewNode[string, string]("test",
			func(ctx context.Context, s string) (string, error) {
				return s, nil
			})

		p, _ := NewPipeline(node)
		ctx := context.Background()

		// Start twice - second call should be no-op due to sync.Once
		err1 := p.Start(ctx)
		err2 := p.Start(ctx)

		if err1 != nil {
			t.Errorf("First start failed: %v", err1)
		}
		if err2 != nil {
			t.Errorf("Second start should be no-op but got error: %v", err2)
		}

		p.Stop(time.Second)
	})

	t.Run("DoubleStop", func(t *testing.T) {
		node := NewNode[string, string]("test",
			func(ctx context.Context, s string) (string, error) {
				return s, nil
			})

		p, _ := NewPipeline(node)
		ctx := context.Background()
		p.Start(ctx)

		// Stop twice - second call should be no-op due to sync.Once
		err1 := p.Stop(time.Second)
		err2 := p.Stop(time.Second)

		if err1 != nil {
			t.Errorf("First stop failed: %v", err1)
		}
		if err2 != nil {
			t.Errorf("Second stop should be no-op but got error: %v", err2)
		}
	})

	t.Run("SendAfterStop", func(t *testing.T) {
		node := NewNode[string, string]("test",
			func(ctx context.Context, s string) (string, error) {
				return s, nil
			})

		p, _ := NewPipeline(node)
		ctx := context.Background()
		p.Start(ctx)
		p.Stop(time.Second)

		// Try to send after stopping
		err := p.SendWithTimeout("test", time.Second)
		if err != ErrPipelineStopped {
			t.Errorf("Expected ErrPipelineStopped, got %v", err)
		}
	})

	t.Run("NilProcessor", func(t *testing.T) {
		// Should panic when creating node with nil processor
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic for nil processor")
			} else {
				t.Logf("Got expected panic: %v", r)
			}
		}()

		NewNode[string, string]("test", nil)
	})

	t.Run("LargeData", func(t *testing.T) {
		// Test with large data to ensure no buffer overflows
		type LargeStruct struct {
			Data [1024 * 1024]byte // 1MB
		}

		node := NewNode[LargeStruct, LargeStruct]("large",
			func(ctx context.Context, data LargeStruct) (LargeStruct, error) {
				return data, nil
			})

		p, _ := NewPipeline(node)
		ctx := context.Background()
		p.Start(ctx)

		large := LargeStruct{}
		err := p.SendWithTimeout(large, 5*time.Second)
		if err != nil {
			t.Errorf("Failed to process large data: %v", err)
		}

		p.Stop(time.Second)
	})
}

// TestNodeMetrics tests metrics collection accuracy
func TestNodeMetrics(t *testing.T) {
	processed := 0
	config := DefaultConfig()

	node := NewNode[int, int]("metrics",
		func(ctx context.Context, i int) (int, error) {
			processed++
			time.Sleep(10 * time.Millisecond)
			return i * 2, nil
		}, config)

	p, _ := NewPipeline(node)
	ctx := context.Background()
	p.Start(ctx)

	const numJobs = 10
	for i := 0; i < numJobs; i++ {
		if err := p.SendWithTimeout(i, time.Second); err != nil {
			t.Errorf("Failed to send job %d: %v", i, err)
		}
	}

	// Wait for processing
	time.Sleep(200 * time.Millisecond)

	metrics := node.Metrics()
	if metrics.ProcessedCount.Load() != int64(numJobs) {
		t.Errorf("Expected %d processed jobs, got %d", numJobs, metrics.ProcessedCount.Load())
	}

	avgLatency := metrics.GetAverageLatency()
	if avgLatency < 10*time.Millisecond || avgLatency > 20*time.Millisecond {
		t.Errorf("Unexpected average latency: %v", avgLatency)
	}

	p.Stop(time.Second)
}
