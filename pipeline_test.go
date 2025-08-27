package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestBasicPipeline tests a simple linear pipeline
func TestBasicPipeline(t *testing.T) {
	// Create nodes
	nodeA := NewNode[string, string]("nodeA",
		func(ctx context.Context, input string) (string, error) {
			return input + " -> A", nil
		})

	nodeB := NewNode[string, string]("nodeB",
		func(ctx context.Context, input string) (string, error) {
			return input + " -> B", nil
		})

	nodeC := NewNode[string, string]("nodeC",
		func(ctx context.Context, input string) (string, error) {
			t.Logf("Final result: %s -> C", input)
			return input + " -> C", nil
		})

	// Connect nodes
	nodeA.Connect(nodeB)
	nodeB.Connect(nodeC)

	// Create and start pipeline
	pipeline, err := NewPipeline(nodeA)
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}

	ctx := context.Background()
	if err := pipeline.Start(ctx); err != nil {
		t.Fatalf("Failed to start pipeline: %v", err)
	}

	// Send test data
	testData := []string{"test1", "test2", "test3"}
	for _, data := range testData {
		if err := pipeline.SendWithTimeout(data, 5*time.Second); err != nil {
			t.Errorf("Failed to process %s: %v", data, err)
		}
	}

	// Graceful shutdown
	if err := pipeline.Stop(10 * time.Second); err != nil {
		t.Errorf("Failed to stop pipeline: %v", err)
	}

	// Check metrics
	metricsA := nodeA.Metrics()
	if metricsA.ProcessedCount.Load() != int64(len(testData)) {
		t.Errorf("Expected %d processed jobs, got %d",
			len(testData), metricsA.ProcessedCount.Load())
	}
}

// TestComplexDAG tests a diamond-shaped DAG
func TestComplexDAG(t *testing.T) {
	//     B -> D
	//    /      \
	//   A        F
	//    \      /
	//     C -> E

	results := sync.Map{}

	nodeA := NewNode[int, int]("A",
		func(ctx context.Context, input int) (int, error) {
			return input * 2, nil
		})

	nodeB := NewNode[int, int]("B",
		func(ctx context.Context, input int) (int, error) {
			return input + 10, nil
		})

	nodeC := NewNode[int, int]("C",
		func(ctx context.Context, input int) (int, error) {
			return input + 20, nil
		})

	nodeD := NewNode[int, int]("D",
		func(ctx context.Context, input int) (int, error) {
			return input * 3, nil
		})

	nodeE := NewNode[int, int]("E",
		func(ctx context.Context, input int) (int, error) {
			return input * 4, nil
		})

	nodeF := NewNode[int, any]("F",
		func(ctx context.Context, input int) (any, error) {
			results.Store(fmt.Sprintf("path-%d", input), true)
			t.Logf("Result received at F: %d", input)
			return nil, nil
		})

	// Build DAG
	nodeA.Connect(nodeB)
	nodeA.Connect(nodeC)
	nodeB.Connect(nodeD)
	nodeC.Connect(nodeE)
	nodeD.Connect(nodeF)
	nodeE.Connect(nodeF)

	// Create and start pipeline
	pipeline, err := NewPipeline(nodeA)
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}

	ctx := context.Background()
	if err := pipeline.Start(ctx); err != nil {
		t.Fatalf("Failed to start pipeline: %v", err)
	}

	// Send test data
	if err := pipeline.SendWithTimeout(5, 5*time.Second); err != nil {
		t.Errorf("Failed to process: %v", err)
	}

	// Give time for processing
	time.Sleep(100 * time.Millisecond)

	// Check that both paths were executed
	count := 0
	results.Range(func(key, value interface{}) bool {
		count++
		return true
	})

	if count != 2 {
		t.Errorf("Expected 2 paths to be executed, got %d", count)
	}

	// Cleanup
	if err := pipeline.Stop(5 * time.Second); err != nil {
		t.Errorf("Failed to stop pipeline: %v", err)
	}
}

// TestErrorHandling tests error handling and retries
func TestErrorHandling(t *testing.T) {
	attempts := atomic.Int32{}

	config := DefaultConfig()
	config.MaxRetries = 2 // Explicitly enable retries for this test
	config.RetryDelay = 10 * time.Millisecond

	nodeA := NewNode[int, int]("nodeA",
		func(ctx context.Context, input int) (int, error) {
			attempt := attempts.Add(1)
			if attempt < 3 {
				return 0, fmt.Errorf("attempt %d failed", attempt)
			}
			return input * 2, nil
		},
		config)

	pipeline, err := NewPipeline(nodeA)
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}

	ctx := context.Background()
	if err := pipeline.Start(ctx); err != nil {
		t.Fatalf("Failed to start pipeline: %v", err)
	}

	// Should succeed after retries
	err = pipeline.SendWithTimeout(10, 5*time.Second)
	if err != nil {
		t.Errorf("Expected success after retries, got error: %v", err)
	}

	if attempts.Load() != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts.Load())
	}

	pipeline.Stop(5 * time.Second)
}

// TestContextCancellation tests proper context handling
func TestContextCancellation(t *testing.T) {
	processed := atomic.Bool{}

	nodeA := NewNode[string, string]("nodeA",
		func(ctx context.Context, input string) (string, error) {
			select {
			case <-time.After(1 * time.Second):
				processed.Store(true)
				return input + " processed", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		})

	pipeline, err := NewPipeline(nodeA)
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}

	ctx := context.Background()
	if err := pipeline.Start(ctx); err != nil {
		t.Fatalf("Failed to start pipeline: %v", err)
	}

	// Send job with short timeout
	err = pipeline.SendWithTimeout("test", 100*time.Millisecond)
	if err == nil {
		t.Error("Expected timeout error")
	}

	if processed.Load() {
		t.Error("Job should not have been processed")
	}

	pipeline.Stop(5 * time.Second)
}

// TestCircuitBreaker tests circuit breaker functionality
func TestCircuitBreaker(t *testing.T) {
	failures := atomic.Int32{}

	config := DefaultConfig()
	config.CircuitBreaker.Enabled = true // Explicitly enable circuit breaker
	config.CircuitBreaker.FailureThreshold = 3
	config.MaxRetries = 0 // No retries for this test

	nodeA := NewNode[int, int]("nodeA",
		func(ctx context.Context, input int) (int, error) {
			failures.Add(1)
			return 0, errors.New("always fails")
		},
		config)

	pipeline, err := NewPipeline(nodeA)
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}

	ctx := context.Background()
	if err := pipeline.Start(ctx); err != nil {
		t.Fatalf("Failed to start pipeline: %v", err)
	}

	// Send jobs until circuit opens
	for i := 0; i < 5; i++ {
		err := pipeline.SendWithTimeout(i, 1*time.Second)
		if err != nil && err.Error() == "circuit breaker open for node nodeA" {
			t.Logf("Circuit breaker opened after %d failures", failures.Load())
			break
		}
	}

	if failures.Load() < 3 {
		t.Error("Circuit breaker should have opened")
	}

	pipeline.Stop(5 * time.Second)
}

// TestCycleDetection tests that cycles are detected
func TestCycleDetection(t *testing.T) {
	nodeA := NewNode[string, string]("A",
		func(ctx context.Context, s string) (string, error) {
			return s, nil
		})

	nodeB := NewNode[string, string]("B",
		func(ctx context.Context, s string) (string, error) {
			return s, nil
		})

	nodeC := NewNode[string, string]("C",
		func(ctx context.Context, s string) (string, error) {
			return s, nil
		})

	// Create cycle: A -> B -> C -> A
	nodeA.Connect(nodeB)
	nodeB.Connect(nodeC)
	nodeC.Connect(nodeA)

	_, err := NewPipeline(nodeA)
	if err == nil {
		t.Error("Expected cycle detection error")
	}

	if err.Error() != "pipeline validation failed: pipeline contains cycle" {
		t.Errorf("Unexpected error: %v", err)
	}
}

// TestHighThroughput tests pipeline under high load
func TestHighThroughput(t *testing.T) {
	processed := atomic.Int64{}

	config := DefaultConfig()
	config.Workers = 8
	config.BufferSize = 100

	nodeA := NewNode[int, int]("nodeA",
		func(ctx context.Context, input int) (int, error) {
			processed.Add(1)
			return input * 2, nil
		},
		config)

	pipeline, err := NewPipeline(nodeA)
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}

	ctx := context.Background()
	if err := pipeline.Start(ctx); err != nil {
		t.Fatalf("Failed to start pipeline: %v", err)
	}

	// Send many jobs concurrently
	jobCount := 1000
	var wg sync.WaitGroup
	errors := atomic.Int32{}

	start := time.Now()

	for i := 0; i < jobCount; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			if err := pipeline.SendWithTimeout(val, 5*time.Second); err != nil {
				errors.Add(1)
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	if errors.Load() > 0 {
		t.Errorf("Had %d errors processing jobs", errors.Load())
	}

	throughput := float64(processed.Load()) / elapsed.Seconds()
	t.Logf("Processed %d jobs in %v (%.0f jobs/sec)",
		processed.Load(), elapsed, throughput)

	// Check metrics
	metrics := nodeA.Metrics()
	avgLatency := metrics.GetAverageLatency()
	t.Logf("Average latency: %v", avgLatency)

	pipeline.Stop(10 * time.Second)
}

// BenchmarkPipeline benchmarks pipeline throughput
func BenchmarkPipeline(b *testing.B) {
	nodeA := NewNode[int, int]("nodeA",
		func(ctx context.Context, input int) (int, error) {
			return input * 2, nil
		})

	nodeB := NewNode[int, int]("nodeB",
		func(ctx context.Context, input int) (int, error) {
			return input + 10, nil
		})

	nodeA.Connect(nodeB)

	pipeline, _ := NewPipeline(nodeA)
	ctx := context.Background()
	pipeline.Start(ctx)
	defer pipeline.Stop(5 * time.Second)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			pipeline.SendWithTimeout(i, time.Second)
			i++
		}
	})
}
