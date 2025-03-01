package pipeline

import (
	"errors"
	"sync"
)

type Pipeline struct {
	root   INode
	once   sync.Once
	doneCh chan struct{} // doneCh is used to notify producer to stop
}

func NewPipeline(root INode) (*Pipeline, error) {
	if root == nil {
		return nil, errors.New("nil root node")
	}
	p := &Pipeline{root: root, once: sync.Once{}, doneCh: make(chan struct{}, 1)}
	if p.hasCycle() {
		return nil, errors.New("pipeline has cycle")
	}
	return p, nil
}

func (p *Pipeline) JobReceiver(job any) error {
	return p.root.jobReceiver(job)
}

// LatestJob returns the latest job been processed by the pipeline.
func (p *Pipeline) LatestJob() any {
	return p.root.latestJob()
}

// Start starts the pipeline
func (p *Pipeline) Start() chan struct{} {
	p.dfs(func(node INode) {
		Debug(node.getName(), " starting")
		node.start()
	})
	return p.doneCh
}

func (p *Pipeline) Stop() {
	go p.once.Do(func() {
		p.doneCh <- struct{}{}
		p.dfs(func(node INode) {
			Debug(node.getName(), " stopping")
			node.stop()
			node.wait()
		})
	})
}

// Wait waits for all nodes to finish.
func (p *Pipeline) Wait() {
	p.dfs(func(node INode) {
		Debug(node.getName(), " waiting")
		node.wait()
	})
}

// dfs route the node graph and execute fn on each node
func (p *Pipeline) dfs(fn func(node INode)) {
	visited := make(map[INode]bool)
	p.dfsHelper(p.root, visited, fn)
}

func (p *Pipeline) dfsHelper(node INode, visited map[INode]bool, fn func(node INode)) {
	if visited[node] {
		return
	}
	visited[node] = true
	fn(node)
	for _, next := range node.getNext() {
		p.dfsHelper(next, visited, fn)
	}
}

// hasCycle checks if the pipeline has cycle within individual pipeline flows
func (p *Pipeline) hasCycle() bool {
	visited := make(map[INode]bool)
	inStack := make(map[INode]bool)

	var dfs func(node INode) bool
	dfs = func(node INode) bool {
		if inStack[node] {
			// Found a cycle in current path
			return true
		}
		if visited[node] {
			// Already visited this node in another path
			return false
		}

		visited[node] = true
		inStack[node] = true

		for _, next := range node.getNext() {
			if dfs(next) {
				return true
			}
		}

		inStack[node] = false
		return false
	}

	return dfs(p.root)
}
