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
	normalize := pipeline.NewNode[string, string]("normalize",
		func(ctx context.Context, text string) (string, error) {
			return strings.Join(strings.Fields(text), " "), nil
		})

	uppercase := pipeline.NewNode[string, string]("uppercase",
		func(ctx context.Context, text string) (string, error) {
			return strings.ToUpper(text), nil
		})

	printer := pipeline.NewNode[string, any]("printer",
		func(ctx context.Context, text string) (any, error) {
			fmt.Println(text)
			return nil, nil
		})

	// Connect nodes: normalize -> uppercase -> printer
	normalize.Connect(uppercase)
	uppercase.Connect(printer)

	// Create and start pipeline
	p, _ := pipeline.NewPipeline(normalize)
	p.Start(context.Background())

	// Process some text
	p.Send("  hello   world  ")

	// Wait for processing to complete
	time.Sleep(100 * time.Millisecond)
	p.Stop(5 * time.Second)

	// Output: HELLO WORLD
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
	p.Start(context.Background())

	// Process: 5 -> 10 -> (20->60) and (30->120)
	p.Send(5)

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
	config.MaxRetries = 2

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
		// Errors during retries are logged here
	})

	// Create pipeline
	p, _ := pipeline.NewPipeline(processor)
	p.Start(context.Background())

	// Process with automatic retries
	if err := p.Send("test"); err != nil {
		fmt.Printf("Final error: %v\n", err)
	} else {
		fmt.Println("Processing succeeded after retries")
	}

	p.Stop(5 * time.Second)

	// Output: Processing succeeded after retries
}
