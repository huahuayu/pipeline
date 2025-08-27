package pipeline_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/huahuayu/pipeline"
)

// Example demonstrates a simple text processing pipeline
func Example() {
	// Create nodes for text processing pipeline
	uppercase := pipeline.NewNode[string, string]("uppercase",
		func(ctx context.Context, text string) (string, error) {
			return strings.ToUpper(text), nil
		})

	wordCount := pipeline.NewNode[string, int]("wordcount",
		func(ctx context.Context, text string) (int, error) {
			return len(strings.Fields(text)), nil
		})

	printer := pipeline.NewNode[int, any]("printer",
		func(ctx context.Context, count int) (any, error) {
			fmt.Printf("Word count: %d\n", count)
			return nil, nil
		})

	// Connect nodes: uppercase -> wordCount -> printer
	uppercase.Connect(wordCount)
	wordCount.Connect(printer)

	// Create and start pipeline
	p, _ := pipeline.NewPipeline(uppercase)
	ctx := context.Background()
	p.Start(ctx)

	// Process some text
	p.SendWithTimeout("hello world from pipeline", 5*time.Second)

	// Graceful shutdown
	p.Stop(5 * time.Second)

	// Output: Word count: 4
}

// Example_complexDAG demonstrates a diamond-shaped DAG pipeline
func Example_complexDAG() {
	// Create a pipeline that processes numbers through multiple paths
	//     add10 -> multiply3
	//    /               \
	// input              output
	//    \               /
	//     add20 -> multiply4

	input := pipeline.NewNode[int, int]("input",
		func(ctx context.Context, n int) (int, error) {
			return n * 2, nil // Double the input
		})

	add10 := pipeline.NewNode[int, int]("add10",
		func(ctx context.Context, n int) (int, error) {
			return n + 10, nil
		})

	add20 := pipeline.NewNode[int, int]("add20",
		func(ctx context.Context, n int) (int, error) {
			return n + 20, nil
		})

	multiply3 := pipeline.NewNode[int, int]("multiply3",
		func(ctx context.Context, n int) (int, error) {
			return n * 3, nil
		})

	multiply4 := pipeline.NewNode[int, int]("multiply4",
		func(ctx context.Context, n int) (int, error) {
			return n * 4, nil
		})

	output := pipeline.NewNode[int, any]("output",
		func(ctx context.Context, n int) (any, error) {
			fmt.Printf("Result: %d\n", n)
			return nil, nil
		})

	// Build the DAG
	input.Connect(add10)
	input.Connect(add20)
	add10.Connect(multiply3)
	add20.Connect(multiply4)
	multiply3.Connect(output)
	multiply4.Connect(output)

	// Create and start pipeline
	p, _ := pipeline.NewPipeline(input)
	ctx := context.Background()
	p.Start(ctx)

	// Process: 5 -> 10 -> (20->60) and (30->120)
	p.SendWithTimeout(5, 5*time.Second)

	// Wait a bit for processing
	time.Sleep(100 * time.Millisecond)

	// Shutdown
	p.Stop(5 * time.Second)

	// Unordered output:
	// Result: 60
	// Result: 120
}

// Example_errorHandling demonstrates error handling with retries
func Example_errorHandling() {
	attempts := 0
	config := pipeline.DefaultConfig()
	config.MaxRetries = 2 // Explicitly enable retries (default is 0)

	// Create a node that fails initially then succeeds
	processor := pipeline.NewNode[string, string]("processor",
		func(ctx context.Context, input string) (string, error) {
			attempts++
			if attempts < 3 {
				return "", fmt.Errorf("attempt %d failed", attempts)
			}
			return "SUCCESS: " + input, nil
		},
		config).WithErrorHandler(func(err error) {
		fmt.Printf("Error handled: %v\n", err)
	})

	// Create pipeline
	p, _ := pipeline.NewPipeline(processor)
	ctx := context.Background()
	p.Start(ctx)

	// Process with automatic retries
	if err := p.SendWithTimeout("test", 5*time.Second); err != nil {
		fmt.Printf("Final error: %v\n", err)
	} else {
		fmt.Println("Processing succeeded after retries")
	}

	p.Stop(5 * time.Second)

	// Output: Processing succeeded after retries
}
