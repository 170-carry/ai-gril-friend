package memory

import (
	"context"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrProcessorClosed 表示异步处理器已关闭，不能再接收新任务。
	ErrProcessorClosed = errors.New("memory async processor is closed")
	// ErrProcessorQueueFull 表示队列已满，当前任务被丢弃。
	ErrProcessorQueueFull = errors.New("memory async processor queue is full")
)

// TurnProcessor 抽象“记忆写入处理器”，可替换为同步/异步实现。
type TurnProcessor interface {
	Enqueue(in TurnInput) error
	Close() error
}

// AsyncProcessorConfig 是异步处理器参数。
type AsyncProcessorConfig struct {
	Workers int
	Queue   int
	Timeout time.Duration
}

// Normalize 对异步处理器配置做边界修正。
func (c AsyncProcessorConfig) Normalize() AsyncProcessorConfig {
	if c.Workers <= 0 {
		c.Workers = 2
	}
	if c.Queue <= 0 {
		c.Queue = 256
	}
	if c.Timeout <= 0 {
		c.Timeout = 8 * time.Second
	}
	return c
}

// AsyncProcessor 使用内存队列 + worker 池异步执行 ProcessTurn，避免阻塞聊天主链路。
type AsyncProcessor struct {
	engine  Engine
	cfg     AsyncProcessorConfig
	queue   chan TurnInput
	wg      sync.WaitGroup
	closed  atomic.Bool
	started atomic.Bool
}

// NewAsyncProcessor 创建并启动异步处理器；engine 为空时返回 nil。
func NewAsyncProcessor(engine Engine, cfg AsyncProcessorConfig) *AsyncProcessor {
	if engine == nil {
		return nil
	}
	cfg = cfg.Normalize()
	p := &AsyncProcessor{
		engine: engine,
		cfg:    cfg,
		queue:  make(chan TurnInput, cfg.Queue),
	}
	p.start()
	return p
}

func (p *AsyncProcessor) start() {
	if !p.started.CompareAndSwap(false, true) {
		return
	}
	for i := 0; i < p.cfg.Workers; i++ {
		p.wg.Add(1)
		go p.workerLoop(i + 1)
	}
}

func (p *AsyncProcessor) workerLoop(workerID int) {
	defer p.wg.Done()
	for in := range p.queue {
		ctx, cancel := context.WithTimeout(context.Background(), p.cfg.Timeout)
		err := p.engine.ProcessTurn(ctx, in)
		cancel()
		if err != nil {
			log.Printf("memory async worker=%d process turn failed user=%s session=%s: %v", workerID, in.UserID, in.SessionID, err)
		}
	}
}

// Enqueue 投递记忆任务；队列满时直接返回错误，由上层决定是否降级丢弃。
func (p *AsyncProcessor) Enqueue(in TurnInput) error {
	if p == nil {
		return nil
	}
	if p.closed.Load() {
		return ErrProcessorClosed
	}
	select {
	case p.queue <- in:
		return nil
	default:
		return ErrProcessorQueueFull
	}
}

// Close 停止接收新任务并等待队列消费完毕。
func (p *AsyncProcessor) Close() error {
	if p == nil {
		return nil
	}
	if !p.closed.CompareAndSwap(false, true) {
		return nil
	}
	close(p.queue)
	p.wg.Wait()
	return nil
}
