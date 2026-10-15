package main

// 内存事件中心：写操作广播 Event 给 SSE 订阅者；Agent 动态保留最近 200 条。

import (
	"sync"
	"time"
)

const (
	activityCap   = 200
	subscriberBuf = 64
)

type hub struct {
	mu       sync.Mutex
	subs     map[chan Event]struct{}
	activity []Activity // 环形缓冲，next 指向下一个写入位置
	next     int
	seq      int64
}

func newHub() *hub { return &hub{subs: map[chan Event]struct{}{}} }

func (h *hub) subscribe() (<-chan Event, func()) {
	ch := make(chan Event, subscriberBuf)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, ch)
			h.mu.Unlock()
			close(ch)
		})
	}
}

// publish 不阻塞：订阅者缓冲满了就丢弃这条。
func (h *hub) publish(ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (h *hub) record(a Activity) Activity {
	h.mu.Lock()
	h.seq++
	a.ID = h.seq
	a.At = time.Now().UTC().Format(time.RFC3339Nano)
	if len(h.activity) < activityCap {
		h.activity = append(h.activity, a)
	} else {
		h.activity[h.next] = a
	}
	h.next = (h.next + 1) % activityCap
	h.mu.Unlock()
	return a
}

// recent 返回最近 limit 条，新的在前。
func (h *hub) recent(limit int) []Activity {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := len(h.activity)
	if limit <= 0 || limit > n {
		limit = n
	}
	out := make([]Activity, 0, limit)
	for i := 1; i <= limit; i++ {
		out = append(out, h.activity[(h.next-i+activityCap)%activityCap])
	}
	return out
}
