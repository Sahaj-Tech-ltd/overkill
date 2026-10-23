package processrunner

import (
	"fmt"
	"sync"
)

// OutputCapture drains arbitrary process output while retaining a bounded
// prefix and suffix. The suffix preserves final diagnostics/completion markers.
type OutputCapture struct {
	mu                 sync.Mutex
	limit              int
	head, tail         []byte
	position, tailSize int
	total              int64
}

func NewOutputCapture(limit int) *OutputCapture {
	if limit < 2 {
		limit = 2
	}
	return &OutputCapture{limit: limit}
}

func (c *OutputCapture) Write(data []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(data)
	c.total += int64(n)
	headLimit := c.limit / 2
	if len(c.head) < headLimit {
		take := min(headLimit-len(c.head), len(data))
		c.head = append(c.head, data[:take]...)
		data = data[take:]
	}
	if len(data) == 0 {
		return n, nil
	}
	tailLimit := c.limit - headLimit
	if c.tail == nil {
		c.tail = make([]byte, tailLimit)
	}
	if len(data) >= tailLimit {
		copy(c.tail, data[len(data)-tailLimit:])
		c.position = 0
		c.tailSize = tailLimit
		return n, nil
	}
	first := copy(c.tail[c.position:], data)
	copy(c.tail, data[first:])
	c.position = (c.position + len(data)) % tailLimit
	c.tailSize = min(tailLimit, c.tailSize+len(data))
	return n, nil
}

func (c *OutputCapture) Snapshot() (string, int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	truncated := c.total > int64(c.limit)
	result := append([]byte(nil), c.head...)
	if truncated {
		result = append(result, fmt.Sprintf("\n[output truncated: %d bytes omitted]\n", c.total-int64(c.limit))...)
	}
	if c.tailSize < len(c.tail) {
		result = append(result, c.tail[:c.tailSize]...)
	} else if c.tailSize > 0 {
		result = append(result, c.tail[c.position:]...)
		result = append(result, c.tail[:c.position]...)
	}
	return string(result), c.total, truncated
}
