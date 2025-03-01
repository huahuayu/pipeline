package pipeline

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"testing"
	"time"
)

var (
	welcomer = NewDefaultNode[string]("welcomer", welcome)
	nodeA    = NewDefaultNode[string]("nodeA", nodeAImpl)
	nodeB    = NewDefaultNode[string]("nodeB", nodeBImpl)
	nodeC    = NewDefaultNode[string]("nodeC", nodeCImpl)
	nodeD    = NewDefaultNode[string]("nodeD", nodeDImpl)
	nodeE    = NewDefaultNode[string]("nodeE", nodeEImpl)
	nodeF    = NewDefaultNode[string]("nodeF", nodeFImpl)
	nodeG    = NewDefaultNode[string]("nodeG", nodeGImpl)
	nodeH    = NewDefaultNode[string]("nodeH", nodeHImpl)
	printer0 = NewDefaultNode[string]("printer0", print0)
	printer1 = NewDefaultNode[string]("printer1", print1)
	printer2 = NewDefaultNode[string]("printer2", print2)
)

func TestPipeline(t *testing.T) {
	SetLevel(DebugLevel)
	welcomer.SetNext(nodeA)
	nodeA.SetNext(nodeB)
	nodeB.SetNext(nodeC)
	nodeC.SetNext(nodeD)
	nodeD.SetNext(printer0)
	nodeA.SetNext(nodeE)
	nodeE.SetNext(nodeF)
	nodeF.SetNext(printer1)
	nodeC.SetNext(nodeG)
	nodeG.SetNext(nodeH)
	nodeH.SetNext(printer2)
	p, err := NewPipeline(welcomer)
	if err != nil {
		t.Fatal(err)
	}
	doneSignal := p.Start()
	go time.AfterFunc(time.Second*5, func() {
		p.Stop()
	})
	producer(10, doneSignal)
	p.Wait()
	fmt.Println("latestJob processed: ", p.LatestJob())
	fmt.Println("exit!")
}

func producer(num int, stopSignal chan struct{}) {
	fmt.Printf("producer started, generate %d name\n", num)
	for i := 0; i < num; i++ {
		select {
		case <-stopSignal:
			fmt.Println("producer stop because of pipeline stop signal")
			return
		default:
			if name, _ := nameGenerator(); name != "" {
				go func(i int, name string) {
					welcomer.jobReceiver(name)
					fmt.Printf("producer send: %d %s\n", i, name)
				}(i, name)
			}
		}
	}
	fmt.Println("producer finished")
}

func welcome(name string) (any, error) {
	time.Sleep(time.Millisecond * time.Duration(1000+rand.Intn(2000)))
	return "Hello " + name, nil
}

func print0(str string) (any, error) {
	fmt.Println(str + " via printer0")
	return nil, nil
}

func print1(str string) (any, error) {
	fmt.Println(str + " via printer1")
	return nil, nil
}
func print2(str string) (any, error) {
	fmt.Println(str + " via printer2")
	return nil, nil
}

func nodeAImpl(str string) (any, error) {
	time.Sleep(time.Millisecond * time.Duration(1000+rand.Intn(2000)))
	return str + " from nodeA", nil
}

func nodeBImpl(str string) (any, error) {
	time.Sleep(time.Millisecond * time.Duration(1000+rand.Intn(2000)))
	return str + " -> nodeB", nil
}

func nodeCImpl(str string) (any, error) {
	time.Sleep(time.Millisecond * time.Duration(1000+rand.Intn(2000)))
	return str + " -> nodeC", nil
}

func nodeDImpl(str string) (any, error) {
	time.Sleep(time.Millisecond * time.Duration(1000+rand.Intn(2000)))
	return str + " -> nodeD", nil
}

func nodeEImpl(str string) (any, error) {
	time.Sleep(time.Millisecond * time.Duration(1000+rand.Intn(2000)))
	return str + " -> nodeE", nil
}

func nodeFImpl(str string) (any, error) {
	time.Sleep(time.Millisecond * time.Duration(1000+rand.Intn(2000)))
	return str + " -> nodeF", nil
}

func nodeGImpl(str string) (any, error) {
	time.Sleep(time.Millisecond * time.Duration(1000+rand.Intn(2000)))
	return str + " -> nodeG", nil
}
func nodeHImpl(str string) (any, error) {
	time.Sleep(time.Millisecond * time.Duration(1000+rand.Intn(2000)))
	return str + " -> nodeH", nil
}

func nameGenerator() (string, error) {
	if resp, err := http.Get("https://api.namefake.com/"); err != nil {
		return "", err
	} else {
		if body, err := io.ReadAll(resp.Body); err != nil {
			return "", err
		} else {
			var res = struct {
				Name string `json:"name"`
			}{}
			err = json.Unmarshal(body, &res)
			if err != nil {
				return "", err
			}
			return res.Name, nil
		}
	}
}

func TestPipeline_HasCycle(t *testing.T) {
	// Test work function
	dummyWork := func(s string) (any, error) {
		return s, nil
	}

	t.Run("no cycle", func(t *testing.T) {
		// A -> B -> C
		nodeA := NewDefaultNode[string]("A", dummyWork)
		nodeB := NewDefaultNode[string]("B", dummyWork)
		nodeC := NewDefaultNode[string]("C", dummyWork)

		nodeA.SetNext(nodeB)
		nodeB.SetNext(nodeC)

		p, _ := NewPipeline(nodeA)
		if p.hasCycle() {
			t.Error("pipeline should not have cycle")
		}
	})

	t.Run("direct cycle", func(t *testing.T) {
		// A -> B -> A (direct cycle)
		nodeA := NewDefaultNode[string]("A", dummyWork)
		nodeB := NewDefaultNode[string]("B", dummyWork)

		nodeA.SetNext(nodeB)
		nodeB.SetNext(nodeA)

		p, err := NewPipeline(nodeA)
		if err == nil || p != nil {
			t.Error("pipeline creation should fail due to cycle")
		}
	})

	t.Run("indirect cycle", func(t *testing.T) {
		// A -> B -> C -> A (indirect cycle)
		nodeA := NewDefaultNode[string]("A", dummyWork)
		nodeB := NewDefaultNode[string]("B", dummyWork)
		nodeC := NewDefaultNode[string]("C", dummyWork)

		nodeA.SetNext(nodeB)
		nodeB.SetNext(nodeC)
		nodeC.SetNext(nodeA)

		p, err := NewPipeline(nodeA)
		if err == nil || p != nil {
			t.Error("pipeline creation should fail due to cycle")
		}
	})

	t.Run("multiple branches without cycle", func(t *testing.T) {
		/*
			A ---> B ---> C
			 \
			  \
			   -> D -> E
		*/
		nodeA := NewDefaultNode[string]("A", dummyWork)
		nodeB := NewDefaultNode[string]("B", dummyWork)
		nodeC := NewDefaultNode[string]("C", dummyWork)
		nodeD := NewDefaultNode[string]("D", dummyWork)
		nodeE := NewDefaultNode[string]("E", dummyWork)

		nodeA.SetNext(nodeB)
		nodeB.SetNext(nodeC)
		nodeA.SetNext(nodeD)
		nodeD.SetNext(nodeE)

		p, err := NewPipeline(nodeA)
		if err != nil {
			t.Error("pipeline should be created successfully")
		}
		if p.hasCycle() {
			t.Error("pipeline should not have cycle")
		}
	})

	t.Run("multiple branches with cycle in one path", func(t *testing.T) {
		/*
			A ---> B ---> C
			 \           ^
			  \          |
			   -> D -----+
		*/
		nodeA := NewDefaultNode[string]("A", dummyWork)
		nodeB := NewDefaultNode[string]("B", dummyWork)
		nodeC := NewDefaultNode[string]("C", dummyWork)
		nodeD := NewDefaultNode[string]("D", dummyWork)

		nodeA.SetNext(nodeB)
		nodeB.SetNext(nodeC)
		nodeA.SetNext(nodeD)
		nodeD.SetNext(nodeC)

		p, err := NewPipeline(nodeA)
		if err != nil {
			t.Error("pipeline should be created successfully")
		}
		if p.hasCycle() {
			t.Error("pipeline should not have cycle as paths are separate")
		}
	})

	t.Run("diamond pattern", func(t *testing.T) {
		/*
			   B
			  / \
			A    D
			  \ /
			   C
		*/
		nodeA := NewDefaultNode[string]("A", dummyWork)
		nodeB := NewDefaultNode[string]("B", dummyWork)
		nodeC := NewDefaultNode[string]("C", dummyWork)
		nodeD := NewDefaultNode[string]("D", dummyWork)

		nodeA.SetNext(nodeB)
		nodeA.SetNext(nodeC)
		nodeB.SetNext(nodeD)
		nodeC.SetNext(nodeD)

		p, err := NewPipeline(nodeA)
		if err != nil {
			t.Error("pipeline should be created successfully")
		}
		if p.hasCycle() {
			t.Error("pipeline should not have cycle")
		}
	})
}
