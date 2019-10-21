package syncer

import (
	"container/heap"
	"errors"
	"sync"

	"github.com/filecoin-project/go-filecoin/block"
)

var errBadPush = errors.New("a programmer is pushing the wrong type to a TargetQueue")
var errBadPop = errors.New("a programmer is not checking targetQueue length before popping")

// NewDispatcher creates a new syncing dispatcher.
func NewDispatcher() *Dispatcher {

	return &Dispatcher{
		targetSet: make(map[string]struct{}),
		targetQ:   NewTargetQueue(),
	}
}

// Dispatcher executes syncing requests
type Dispatcher struct {
	// The dispatcher maintains a targeting system for determining the
	// current best syncing target
	// targetMu protects the targeting system
	targetMu sync.Mutex
	// targetSet tracks all tipsetkeys currently being targeted to prevent
	// pushing duplicates to the target queue
	targetSet map[string]struct{}
	// targetQ is a priority queue of target tipsets
	targetQ *TargetQueue
}

// ReceiveHello handles chain information from bootstrap peers.
func (d *Dispatcher) ReceiveHello(ci block.ChainInfo) error { return d.receive(ci) }

// ReceiveOwnBlock handles chain info from a node's own mining system
func (d *Dispatcher) ReceiveOwnBlock(ci block.ChainInfo) error { return d.receive(ci) }

// ReceiveGossipBlock handles chain info from new blocks sent on pubsub
func (d *Dispatcher) ReceiveGossipBlock(ci block.ChainInfo) error { return d.receive(ci) }

func (d *Dispatcher) receive(ci block.ChainInfo) error {
	d.targetMu.Lock()
	defer d.targetMu.Unlock()

	_, targeting := d.targetSet[ci.Head.String()]
	if targeting {
		// already tracking drop quickly
		return nil
	}
	err := d.targetQ.Push(&SyncRequest{ChainInfo: ci})
	if err != nil {
		return err
	}
	d.targetSet[ci.Head.String()] = struct{}{}
	return nil
}

// SyncRequest tracks a logical request of the syncing subsystem to run a
// syncing job against given inputs. syncRequests are created by the
// Dispatcher by inspecting incoming hello messages from bootstrap peers
// and gossipsub block propagations.
type SyncRequest struct {
	block.ChainInfo
	// needed by internal container/heap methods for maintaining sort
	index int
}

// rawQueue orders the dispatchers syncRequests by a policy.
// The current simple policy is to order syncing requests by claimed chain
// height.
//
// rawQueue can panic so it shouldn't be used unwrapped
type rawQueue []*SyncRequest

// Heavily inspired by https://golang.org/pkg/container/heap/
func (rq rawQueue) Len() int { return len(rq) }

func (rq rawQueue) Less(i, j int) bool {
	// We want Pop to give us the highest priority so we use greater than
	return rq[i].Height > rq[j].Height
}

func (rq rawQueue) Swap(i, j int) {
	rq[i], rq[j] = rq[j], rq[i]
	rq[i].index = j
	rq[j].index = i
}

func (rq *rawQueue) Push(x interface{}) {
	n := len(*rq)
	syncReq := x.(*SyncRequest)
	syncReq.index = n
	*rq = append(*rq, syncReq)
}

func (rq *rawQueue) Pop() interface{} {
	old := *rq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil  // avoid memory leak
	item.index = -1 // for safety
	*rq = old[0 : n-1]
	return item
}

// TargetQueue orders dispatcher syncRequests by the underlying rawQueue's
// policy. It exposes programmer errors as return values instead of panicing.
// Callers should check that length is greater than 0 before popping
type TargetQueue struct {
	q rawQueue
}

// NewTargetQueue returns a new target queue with an initialized rawQueue
func NewTargetQueue() *TargetQueue {
	rq := make(rawQueue, 0)
	heap.Init(&rq)
	return &TargetQueue{q: rq}
}

// Push adds a sync request to the target queue.
func (tq *TargetQueue) Push(req *SyncRequest) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errBadPush
		}
	}()
	heap.Push(&tq.q, req)
	return nil
}

// Pop removes and returns the highest priority syncing target.
func (tq *TargetQueue) Pop() (req *SyncRequest, err error) {
	defer func() {
		if r := recover(); r != nil {
			req = nil
			err = errBadPop
		}
	}()
	return heap.Pop(&tq.q).(*SyncRequest), nil
}

// Len returns the number of targets in the queue.
func (tq *TargetQueue) Len() int {
	return len(tq.q)
}
