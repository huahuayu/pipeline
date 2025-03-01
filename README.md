# Pipeline

Pipeline is a high-performance, concurrent data processing library for Go that enables you to build flexible pipelines.

It's an implementation for this original thought: [How to design a pipeline in go](https://liushiming.cn/article/how-to-design-a-pipeline-in-go.html).

![](https://cdn.liushiming.cn/img/20221007130328.png)

## Features

- **High Concurrency**: Process data in parallel with configurable worker pools at each pipeline stage
- **Type Safety with Generics**: Leverage Go's generics for compile-time type checking between pipeline stages
- **Graceful Shutdown**: Clean termination that waits for in-progress jobs to complete
- **Cycle Detection**: Automatic detection and prevention of circular dependencies that could cause deadlocks
- **Flexible Error Handling**: Customize error handling with your own error handler functions
- **Job Tracking**: Monitor pipeline progress by retrieving the latest job processed (can be used to log break point)
- **Directed Acyclic Graph (DAG)**: Complex pipeline with multi branchs are supported

## Key Components

### Pipeline Structure

- Implements a DAG (Directed Acyclic Graph) architecture
- Provides cycle detection to prevent deadlocks
- Supports complex topologies with multiple branches

### Node Implementation

- Uses Go generics for type safety between pipeline stages
- Each node has:
  - A processing function
  - Configurable worker pool size
  - Job queue with configurable size
  - Error handling capabilities
  - Ability to track the latest processed job

### Concurrency Model

- Uses Go channels for communication between nodes
- Implements worker pools for parallel processing
- Provides graceful shutdown mechanism
- Uses sync.WaitGroup for coordinating workers

### Logging System

- Four log levels: Trace, Debug, Info, Error
- Configurable log level

## Installation

```bash
go get github.com/huahuayu/pipeline
```

## Quick Start

Create a simple pipeline `A ─> B ─> C` then start, stop & wait for grace shutdown.

```go
package main

import (
  "fmt"
  "time"

  "github.com/huahuayu/pipeline"
)

func main() {
  // Create processing nodes
  nodeA := pipeline.NewDefaultNode[string]("nodeA", func(input string) (any, error) {
    return input + " processed by A", nil
  })

  nodeB := pipeline.NewDefaultNode[string]("nodeB", func(input string) (any, error) {
    return input + " -> B", nil
  })

  nodeC := pipeline.NewDefaultNode[string]("nodeC", func(input string) (any, error) {
    fmt.Println("Result:", input+" -> C")
    return nil, nil
  })

  // Connect nodes
  nodeA.SetNext(nodeB)
  nodeB.SetNext(nodeC)

  // Create and start the pipeline
  p, err := pipeline.NewPipeline(nodeA)
  if err != nil {
    panic(err)
  }

  p.Start()
  // Send jobs to the pipeline
  p.JobReceiver("Job 1")
  p.JobReceiver("Job 2")
  p.JobReceiver("Job 3")

  // Wait for a while to let jobs process
  time.Sleep(2 * time.Second)

  // Stop the pipeline
  p.Stop()
  p.Wait()

  fmt.Println("Pipeline processing complete")
}
```

## Advanced Usage

### Creating Complex Topologies

The pipeline library supports Directed Acyclic Graph (DAG) structures, allowing you to create advanced processing workflows where data can flow through multiple parallel paths.

For example, you can create a branche topology where:

- Data starts at node A
- Flows through two parallel branches (B-D and C-E-F)

```
    ┌── B ──► D
    │
A ──┴── C ─── E ───► F
```

Here's how to construct this topology in code:

```go
// First create all nodes with their processing functions
nodeA := pipeline.NewDefaultNode[InputType]("nodeA", nodeAProcessingFunc)
nodeB := pipeline.NewDefaultNode[TypeFromA]("nodeB", nodeBProcessingFunc)
nodeC := pipeline.NewDefaultNode[TypeFromA]("nodeC", nodeCProcessingFunc)
nodeD := pipeline.NewDefaultNode[TypeFromB]("nodeD", nodeDProcessingFunc)
nodeE := pipeline.NewDefaultNode[TypeFromC]("nodeE", nodeEProcessingFunc)
nodeF := pipeline.NewDefaultNode[TypeFromDorE]("nodeF", nodeFProcessingFunc)

// Then connect the nodes according to the desired topology
nodeA.SetNext(nodeB)  // A outputs to B
nodeA.SetNext(nodeC)  // A also outputs to C
nodeB.SetNext(nodeD)  // B outputs to D
nodeC.SetNext(nodeE)  // C outputs to E
nodeE.SetNext(nodeF)  // E outputs to F
```

Data will flow through all possible paths.

### Custom Worker Pool Configuration

Customize the number of workers and job queue size:

```go
// Create a node with 5 workers and a job queue size of 10
nodeA := pipeline.NewNode[string]("nodeA", processFunc, 10, 5, nil)
```

### Custom Error Handling

Provide custom error handling logic:

```go
errorHandler := func(err error) {
    // Custom error handling logic
    log.Printf("Error in pipeline: %v", err)
    metrics.IncrementErrorCounter()
}

nodeA := pipeline.NewNode[string]("nodeA", processFunc, 10, 5, errorHandler)
```

## API Reference

### Node Creation

```go
// Create a node with default configuration
NewDefaultNode[T any](name string, workFn func(T) (any, error), onError ...func(error)) *Node[T]

// Create a node with custom configuration
NewNode[T any](name string, workFn func(T) (any, error), jobPoolSize int, workerPoolSize int, onError ...func(error)) *Node[T]
```

### Pipeline Management

```go
// Create a new pipeline with a root node
NewPipeline(root INode) (*Pipeline, error)

// Start the pipeline
Start() chan struct{}

// Stop the pipeline
Stop()

// Wait for all nodes to finish processing
Wait()

// Get the latest job processed by the pipeline
LatestJob() any
```

### Logging

```go
// Set the log level
SetLevel(l LogLevel)

// Log levels
TraceLevel
DebugLevel
InfoLevel
ErrorLevel
```

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

Please make sure your code passes all tests.

## License

This project is licensed under the MIT License.
