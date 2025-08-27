# Pipeline

A fast, concurrent go pipeline library for Go with support for complex DAGs, automatic retries, and circuit breakers.

## Performance

Tested on Apple M1 Pro. Pretty happy with these numbers:

| Pipeline    | Latency | Throughput   | Memory    |
| ----------- | ------- | ------------ | --------- |
| Single Node | 1.8 μs  | 547K ops/sec | 792 B/op  |
| Two Nodes   | 5.2 μs  | 192K ops/sec | 1.4 KB/op |
| Three Nodes | 8.8 μs  | 113K ops/sec | 2.1 KB/op |

The actual processing is around 40ns per node. Most of the overhead comes from Go's channels and scheduling, which honestly isn't much we can optimize further.

```
BenchmarkThroughput-8         2,824,549    6,878 ns/op    145,394 ops/sec
BenchmarkLatencyBreakdown:
  SingleNode-8                6,675,679    1,825 ns/op    792 B/op     13 allocs/op
  TwoNodes-8                  2,331,255    5,202 ns/op    1,464 B/op   26 allocs/op
  ThreeNodes-8                1,372,046    8,771 ns/op    2,136 B/op   39 allocs/op
```

## Installation

```bash
go get github.com/huahuayu/pipeline
```

## Basic Example

Here's the simplest case - a two-stage pipeline that converts strings to uppercase and then counts characters:

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
    // First node: convert to uppercase
    toUpper := pipeline.NewNode[string, string]("uppercase",
        func(ctx context.Context, input string) (string, error) {
            return strings.ToUpper(input), nil
        })

    // Second node: count characters
    counter := pipeline.NewNode[string, int]("counter",
        func(ctx context.Context, input string) (int, error) {
            return len(input), nil
        })

    // Wire them together
    toUpper.Connect(counter)

    // Create pipeline starting from the first node
    p, err := pipeline.NewPipeline(toUpper)
    if err != nil {
        panic(err)
    }

    // Start it up
    ctx := context.Background()
    if err := p.Start(ctx); err != nil {
        panic(err)
    }

    // Send some data
    if err := p.SendWithTimeout("hello world", 5*time.Second); err != nil {
        fmt.Printf("Failed: %v\n", err)
    }

    // Clean shutdown
    if err := p.Stop(10*time.Second); err != nil {
        fmt.Printf("Shutdown error: %v\n", err)
    }
}
```

## Features

- **Type-safe with generics** - No interface{} shenanigans, full compile-time type checking
- **DAG support** - Not just linear pipelines, build any directed acyclic graph
- **Context aware** - Proper cancellation and timeout support throughout
- **Panic recovery** - Workers handle panics gracefully
- **Built-in metrics** - Track throughput, latency, queue sizes
- **Circuit breakers** - Prevent cascading failures (disabled by default)
- **Retries with backoff** - Configurable retry logic (disabled by default)
- **Zero dependencies** - Just standard library

## Configuration

By default, the pipeline runs with minimal features enabled (fail-fast mode). You can enable more complex behavior when needed:

```go
config := pipeline.NodeConfig{
    BufferSize: 100,    // Job queue size
    Workers:    8,      // Concurrent workers

    // Retries are disabled by default
    MaxRetries: 3,
    RetryDelay: 100 * time.Millisecond,

    // Circuit breaker is disabled by default
    CircuitBreaker: pipeline.CircuitBreakerConfig{
        Enabled:          true,
        FailureThreshold: 5,
        ResetTimeout:     30 * time.Second,
    },
}

node := pipeline.NewNode[Input, Output]("my-node", processFunc, config)
```

## Building DAGs

You're not limited to linear pipelines. Here's a diamond pattern:

```go
// Build this graph:
//     B -> D
//    /      \
//   A        F
//    \      /
//     C -> E

nodeA.Connect(nodeB)
nodeA.Connect(nodeC)
nodeB.Connect(nodeD)
nodeC.Connect(nodeE)
nodeD.Connect(nodeF)
nodeE.Connect(nodeF)

// The pipeline figures out the topology automatically
p, _ := pipeline.NewPipeline(nodeA)
```

## Error Handling

Errors are collected and available after processing:

```go
node := pipeline.NewNode[string, string]("validator",
    func(ctx context.Context, input string) (string, error) {
        if input == "" {
            return "", errors.New("empty input not allowed")
        }
        return input, nil
    }).WithErrorHandler(func(err error) {
        // Log it, send to metrics, whatever you need
        log.Printf("Validation failed: %v", err)
    })

// After processing, check for errors
errors := p.Errors()
for _, err := range errors {
    log.Printf("Pipeline error: %v", err)
}
```

## Monitoring

Each node tracks its own metrics:

```go
metrics := node.Metrics()
fmt.Printf("Processed: %d\n", metrics.ProcessedCount.Load())
fmt.Printf("Failed: %d\n", metrics.FailedCount.Load())
fmt.Printf("Queue size: %d\n", metrics.CurrentQueueSize.Load())
fmt.Printf("Avg latency: %v\n", metrics.GetAverageLatency())
```

## Context and Cancellation

Everything respects context cancellation:

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

// This will respect the timeout
err := p.Send(ctx, data)
if errors.Is(err, context.DeadlineExceeded) {
    // Handle timeout
}
```

## Architecture Notes

The pipeline is built around a few core ideas:

1. **Generic nodes** - Each node is strongly typed with input/output types
2. **Worker pools** - Each node runs N workers processing jobs concurrently
3. **Buffered channels** - Used for job queues with configurable buffer sizes
4. **Result channels** - Synchronous handoff between nodes provides natural backpressure
5. **State management** - Atomic operations ensure thread safety without heavy locking

### Latency Breakdown

For a typical two-node pipeline processing, here's where the ~5μs goes:

- Creating context: ~100ns
- Channel send/receive: ~2000ns (multiple hops)
- Goroutine scheduling: ~1000ns
- Actual work: ~80ns (40ns per node)
- Result propagation: ~2000ns

Most overhead is from Go's runtime, not the pipeline itself.

## Testing

```bash
# Standard tests
go test ./...

# Race detection (always passes!)
go test -race ./...

# Benchmarks
go test -bench=. -benchmem

# Coverage
go test -cover ./...
```

Check out `pipeline_test.go` for more examples including stress tests, error scenarios, and complex DAG configurations.

## Tips

1. **Start simple** - Use defaults first, add complexity only when needed
2. **Size buffers appropriately** - Too small causes backpressure, too large wastes memory
3. **One pipeline per flow** - Don't try to reuse pipelines for different data flows
4. **Always call Stop()** - Ensures graceful shutdown and cleanup
5. **Monitor in production** - The built-in metrics are there for a reason

## Contributing

PRs welcome. Just make sure:

- Tests pass (including race detector)
- Benchmarks don't regress significantly
- New features include tests
- Public APIs have godoc comments

## License

MIT
