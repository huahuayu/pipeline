# Pipeline

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/huahuayu/pipeline)](https://goreportcard.com/report/github.com/huahuayu/pipeline)

A high-performance pipeline library for Go.

> 💡 Design article: [How to design a pipeline in Go](https://liushiming.cn/article/how-to-design-a-pipeline-in-go.html)

![Pipeline Architecture](https://cdn.liushiming.cn/img/20221007130328.png)

## Table of Contents

- [Installation](#installation)
- [Quick Start](#quick-start)
- [Core Concepts](#core-concepts)
- [Configuration](#configuration)
- [Building DAGs](#building-dags)
- [Error Handling](#error-handling)
- [Metrics & Monitoring](#metrics--monitoring)
- [Performance](#performance)
- [API Reference](#api-reference)
- [Testing](#testing)
- [Contributing](#contributing)

## Installation

```bash
go get github.com/huahuayu/pipeline
```

**Requirements:** Go 1.25+

## Quick Start

### Pipeline Example

```go
package main

import (
    "context"
    "fmt"
    "strings"
    "time"

    "github.com/huahuayu/pipeline"
)

func main() {
    // Stage 1: Trim and normalize whitespace
    normalize := pipeline.NewNode[string, string]("normalize",
        func(ctx context.Context, s string) (string, error) {
            return strings.Join(strings.Fields(s), " "), nil
        })

    // Stage 2: Convert to uppercase
    toUpper := pipeline.NewNode[string, string]("uppercase",
        func(ctx context.Context, s string) (string, error) {
            return strings.ToUpper(s), nil
        })

    // Stage 3: Print result
    printer := pipeline.NewNode[string, any]("printer",
        func(ctx context.Context, s string) (any, error) {
            fmt.Printf("Result: %s\n", s)
            return nil, nil
        })

    // Connect: normalize -> toUpper -> printer
    normalize.Connect(toUpper)
    toUpper.Connect(printer)

    // Create and run
    p, _ := pipeline.NewPipeline(normalize)
    p.Start(context.Background())
    defer p.Stop(5 * time.Second)

    p.Send("  hello   world  ")
    // Output: Result: HELLO WORLD
}
```

## Core Concepts

### Node

A **Node** is the basic processing unit. Each node:

- Has a typed input and output (`Node[In, Out]`)
- Runs a pool of concurrent workers
- Processes jobs from an internal queue

```go
node := pipeline.NewNode[InputType, OutputType](
    "node-name",                                    // Name for debugging
    func(ctx context.Context, in InputType) (OutputType, error) {
        // Your processing logic
        // ctx is cancelled when pipeline stops
        return result, nil
    },
    config,  // Optional: custom configuration
)
```

### Pipeline

A **Pipeline** orchestrates multiple nodes:

- Validates the graph structure (detects cycles)
- Manages lifecycle (start/stop)
- Handles graceful shutdown

```go
p, err := pipeline.NewPipeline(rootNode)
p.Start(context.Background())
p.Send(data)
p.Stop(timeout)
```

### Data Flow

```
Send() ──► Node A ──► Node B ──► Node C
              │
              └──────► Node D ──► Node E
```

Data flows from the root node to all connected downstream nodes. Multiple connections create a fan-out pattern.

## Configuration

### Default Configuration

```go
// Defaults (based on runtime.NumCPU())
config := pipeline.DefaultConfig()
// BufferSize: NumCPU * 4
// Workers:    NumCPU
// MaxRetries: 0 (disabled)
// RetryDelay: 100ms
```

### Custom Configuration

```go
config := pipeline.NodeConfig{
    BufferSize: 100,                     // Job queue capacity
    Workers:    8,                       // Concurrent workers
    MaxRetries: 3,                       // Retry attempts (0 = disabled)
    RetryDelay: 100 * time.Millisecond,  // Base delay (exponential backoff)

    CircuitBreaker: pipeline.CircuitBreakerConfig{
        Enabled:          true,          // Enable circuit breaker
        FailureThreshold: 5,             // Open after 5 failures
        ResetTimeout:     30 * time.Second, // Try again after 30s
    },
}

node := pipeline.NewNode[In, Out]("name", processor, config)
```

### Configuration Tips

| Setting      | Low Value                            | High Value                     |
| ------------ | ------------------------------------ | ------------------------------ |
| `BufferSize` | Lower memory, may cause backpressure | Higher throughput, more memory |
| `Workers`    | Lower CPU usage                      | Higher parallelism             |
| `MaxRetries` | Fail fast                            | More resilient, higher latency |

## Building DAGs

### Linear Pipeline

```go
A.Connect(B)
B.Connect(C)
// A -> B -> C
```

### Fan-Out (One to Many)

```go
A.Connect(B)
A.Connect(C)
// A -> B
// └──> C
```

### Diamond Pattern

```go
//     B -> D
//    /      \
//   A        F
//    \      /
//     C -> E

A.Connect(B)
A.Connect(C)
B.Connect(D)
C.Connect(E)
D.Connect(F)
E.Connect(F)

p, _ := pipeline.NewPipeline(A)  // Validates: no cycles allowed
```

### Cycle Detection

The pipeline automatically detects and rejects cycles:

```go
A.Connect(B)
B.Connect(C)
C.Connect(A)  // Creates cycle!

_, err := pipeline.NewPipeline(A)
// err: "pipeline validation failed: pipeline contains cycle"
```

## Error Handling

### Per-Node Error Handler

```go
node := pipeline.NewNode[string, string]("validator",
    func(ctx context.Context, input string) (string, error) {
        if input == "" {
            return "", errors.New("empty input")
        }
        return input, nil
    }).WithErrorHandler(func(err error) {
        log.Printf("Validation error: %v", err)
    })
```

### Processing Errors

```go
err := p.Send(data)
if err != nil {
    if errors.Is(err, pipeline.ErrPipelineStopped) {
        // Pipeline was stopped
    } else {
        // Processing error
    }
}
```

### Retries with Exponential Backoff

```go
config := pipeline.NodeConfig{
    MaxRetries: 3,                       // 3 retries = 4 total attempts
    RetryDelay: 100 * time.Millisecond,  // 100ms, 200ms, 400ms
}

// Retry delays: 100ms -> 200ms -> 400ms (exponential backoff)
```

### Circuit Breaker

Prevents cascading failures:

```go
config := pipeline.NodeConfig{
    CircuitBreaker: pipeline.CircuitBreakerConfig{
        Enabled:          true,
        FailureThreshold: 5,              // Open after 5 consecutive failures
        ResetTimeout:     30 * time.Second, // Try half-open after 30s
    },
}

// States: Closed -> Open -> Half-Open -> Closed
//         (normal)  (fail fast) (test one) (recovered)
```

## Async Mode

By default, `p.Send()` blocks until the item traverses the entire pipeline. For high-throughput scenarios where you want "fire-and-forget" behavior, enable Async Mode:

```go
config := pipeline.NodeConfig{
    Workers: 4,
    Async:   true, // Enable non-blocking execution
}

node := pipeline.NewNode[int, int]("worker", processor, config)
    .WithErrorHandler(func(err error) {
        // Handle errors asynchronously
        log.Printf("Processing failed: %v", err)
    })

// Returns immediately (nil if queue has space)
err := p.Send(data)
```

## Graceful Shutdown

The pipeline supports graceful shutdown to ensure all queued jobs are processed before exiting.

```go
// Stop accepting new jobs and wait for all queued jobs to complete
// Pass 0 or a negative value to wait indefinitely.
// Pass a positive duration to force stop after a timeout.
err := p.Stop(0)
if err != nil {
    // Timeout exceeded (if timeout > 0) or other error
}
```

The shutdown process:

1.  `p.Stop()` sets the pipeline state to `Stopping`.
2.  `p.Send()` immediately returns `ErrPipelineStopped`.
3.  The pipeline waits for the root node to drain its input queue.
4.  Shutdown signals propagate through the DAG, ensuring downstream nodes finish their work.
5.  Once all nodes are idle and queues are empty, `p.Stop()` returns.

## Metrics & Monitoring

Each node tracks real-time metrics:

```go
metrics := node.Metrics()

// Counters
processed := metrics.ProcessedCount.Load()  // Successful jobs
failed := metrics.FailedCount.Load()        // Failed jobs
queueSize := metrics.CurrentQueueSize.Load() // Current queue depth

// Latency
avgLatency := metrics.GetAverageLatency()   // Average processing time

// Example: Expose to Prometheus
prometheus.Gauge("pipeline_processed_total").Set(float64(processed))
prometheus.Gauge("pipeline_queue_size").Set(float64(queueSize))
prometheus.Histogram("pipeline_latency_seconds").Observe(avgLatency.Seconds())
```

## Performance

Tested on Apple M1 Pro:

| Pipeline    | Latency | Throughput   | Memory    |
| ----------- | ------- | ------------ | --------- |
| Single Node | 1.8 μs  | 547K ops/sec | 792 B/op  |
| Two Nodes   | 5.2 μs  | 192K ops/sec | 1.4 KB/op |
| Three Nodes | 8.8 μs  | 113K ops/sec | 2.1 KB/op |

```
BenchmarkThroughput-8         2,824,549    6,878 ns/op    145,394 ops/sec
BenchmarkLatencyBreakdown:
  SingleNode-8                6,675,679    1,825 ns/op    792 B/op     13 allocs/op
  TwoNodes-8                  2,331,255    5,202 ns/op    1,464 B/op   26 allocs/op
  ThreeNodes-8                1,372,046    8,771 ns/op    2,136 B/op   39 allocs/op
```

### Where Time Goes

For a two-node pipeline (~5μs):

- Channel operations: ~2000ns
- Goroutine scheduling: ~1000ns
- Result propagation: ~2000ns
- Actual processing: ~80ns

Most overhead is Go runtime, not the pipeline.

## API Reference

### Pipeline

| Method                                       | Description                    |
| -------------------------------------------- | ------------------------------ |
| `NewPipeline(root INode) (*Pipeline, error)` | Create pipeline with root node |
| `Start(ctx context.Context) error`           | Start all nodes                |
| `Stop(timeout time.Duration) error`          | Graceful shutdown              |
| `Send(job any) error`                        | Send job to pipeline           |
| `IsRunning() bool`                           | Check if running               |

### Node

| Method                                         | Description         |
| ---------------------------------------------- | ------------------- |
| `NewNode[In, Out](name, processor, config...)` | Create new node     |
| `Connect(next INode)`                          | Add downstream node |
| `WithErrorHandler(handler func(error))`        | Set error callback  |
| `Metrics() *NodeMetrics`                       | Get metrics         |
| `Name() string`                                | Get node name       |

### NodeConfig Fields

| Field            | Type     | Default  | Description              |
| ---------------- | -------- | -------- | ------------------------ |
| `BufferSize`     | int      | NumCPU×4 | Job queue capacity       |
| `Workers`        | int      | NumCPU   | Concurrent workers       |
| `MaxRetries`     | int      | 0        | Retry attempts           |
| `RetryDelay`     | Duration | 100ms    | Base retry delay         |
| `CircuitBreaker` | Config   | disabled | Circuit breaker settings |

## Testing

```bash
# Run all tests
go test ./...

# With race detection
go test -race ./...

# Benchmarks
go test -bench=. -benchmem

# Coverage report
go test -cover ./...

# Verbose with specific test
go test -v -run TestComplexDAG
```

## Best Practices

1. **Start simple** — Use `DefaultConfig()` first, tune later
2. **Size buffers wisely** — Match to your throughput needs
3. **Always defer Stop()** — Ensures cleanup even on panics
4. **Monitor metrics** — Track queue sizes to detect bottlenecks
5. **Handle errors** — Use `WithErrorHandler` for observability

## Features

- ✅ **Type-safe generics** — Full compile-time type checking
- ✅ **DAG support** — Build any directed acyclic graph
  - ✅ **Async Mode** — Non-blocking execution for high throughput
  - ✅ **Simple API** — Just `Start(ctx)`, `Send()`, `Stop()`
- ✅ **Panic recovery** — Workers handle panics gracefully
- ✅ **Built-in metrics** — Throughput, latency, queue sizes
- ✅ **Circuit breakers** — Prevent cascading failures
- ✅ **Exponential backoff** — Smart retry logic
- ✅ **Zero dependencies** — Only standard library

## Contributing

PRs welcome! Please ensure:

- [ ] Tests pass: `go test -race ./...`
- [ ] Benchmarks don't regress: `go test -bench=.`
- [ ] New features have tests
- [ ] Public APIs have godoc comments

## License

MIT
