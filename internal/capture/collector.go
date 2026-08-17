package capture

import (
	"sync"
	"unicode/utf8"
)

const (
	defaultHeadBytes = 64 << 10
	defaultTailBytes = 192 << 10
	utf8ContextBytes = utf8.UTFMax - 1
)

// streamSnapshot is the bounded byte representation retained for one stream.
// When omitted is zero, head contains the complete stream and tail is empty.
type streamSnapshot struct {
	received int64
	head     []byte
	omitted  int64
	tail     []byte
}

// streamCollector implements io.Writer while retaining a constant amount of
// memory. It keeps the complete stream until the combined budget is exceeded;
// only then does it split the retained bytes into a fixed prefix and suffix.
type streamCollector struct {
	mu        sync.Mutex
	headLimit int
	tailLimit int
	received  int64
	truncated bool

	full []byte
	head []byte
	tail byteRing
}

func newStreamCollector(headLimit, tailLimit int) *streamCollector {
	if headLimit < 0 || tailLimit < 0 {
		panic("capture limits must not be negative")
	}
	return &streamCollector{
		headLimit: headLimit,
		tailLimit: tailLimit,
		full:      make([]byte, 0, headLimit+tailLimit),
	}
}

func (c *streamCollector) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	written := len(p)
	if written == 0 {
		return 0, nil
	}
	c.received += int64(written)

	if c.truncated {
		c.tail.write(p)
		return written, nil
	}

	budget := c.headLimit + c.tailLimit
	if len(c.full)+written <= budget {
		c.full = append(c.full, p...)
		return written, nil
	}

	c.truncated = true
	c.head = make([]byte, 0, c.headLimit)
	c.head = appendPrefix(c.head, c.full, c.headLimit)
	c.head = appendPrefix(c.head, p, c.headLimit)

	c.tail = newByteRing(c.tailLimit + utf8ContextBytes)
	c.tail.write(c.full)
	c.tail.write(p)
	c.full = nil
	return written, nil
}

func appendPrefix(dst, src []byte, limit int) []byte {
	remaining := limit - len(dst)
	if remaining <= 0 {
		return dst
	}
	if len(src) > remaining {
		src = src[:remaining]
	}
	return append(dst, src...)
}

// snapshot is non-destructive: callers may inspect progress and continue
// writing to the same collector.
func (c *streamCollector) snapshot() streamSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.truncated {
		return streamSnapshot{
			received: c.received,
			head:     append([]byte(nil), c.full...),
		}
	}

	head := append([]byte(nil), c.head...)
	window := c.tail.bytes()
	tailStart := len(window) - c.tailLimit
	if tailStart < 0 {
		tailStart = 0
	}
	context := window[:tailStart]
	tail := append([]byte(nil), window[tailStart:]...)
	omitted := c.received - int64(len(head)) - int64(len(tail))

	if trim := incompleteUTF8Suffix(head); trim > 0 {
		head = head[:len(head)-trim]
		omitted += int64(trim)
	}
	if skip := splitUTF8Prefix(context, tail); skip > 0 {
		tail = tail[skip:]
		omitted += int64(skip)
	}

	return streamSnapshot{
		received: c.received,
		head:     head,
		omitted:  omitted,
		tail:     tail,
	}
}

// retainedCapacity exposes the collector's persistent byte capacity to tests.
// The transition between representations may briefly allocate both bounded
// forms, but retained state never grows with the input stream.
func (c *streamCollector) retainedCapacity() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return cap(c.full) + cap(c.head) + len(c.tail.buf)
}

func incompleteUTF8Suffix(value []byte) int {
	start := len(value) - utf8ContextBytes
	if start < 0 {
		start = 0
	}
	for i := start; i < len(value); i++ {
		if validIncompleteUTF8Prefix(value[i:]) {
			return len(value) - i
		}
	}
	return 0
}

func validIncompleteUTF8Prefix(value []byte) bool {
	if len(value) == 0 {
		return false
	}

	want := utf8SequenceLength(value[0])
	if want < 2 || len(value) >= want {
		return false
	}
	for i := 1; i < len(value); i++ {
		if value[i] < 0x80 || value[i] > 0xbf {
			return false
		}
	}

	if len(value) >= 2 {
		second := value[1]
		switch value[0] {
		case 0xe0:
			return second >= 0xa0
		case 0xed:
			return second <= 0x9f
		case 0xf0:
			return second >= 0x90
		case 0xf4:
			return second <= 0x8f
		}
	}
	return true
}

func utf8SequenceLength(first byte) int {
	switch {
	case first >= 0xc2 && first <= 0xdf:
		return 2
	case first >= 0xe0 && first <= 0xef:
		return 3
	case first >= 0xf0 && first <= 0xf4:
		return 4
	default:
		return 0
	}
}

// splitUTF8Prefix returns the number of tail bytes that complete a valid rune
// beginning immediately before the tail boundary. Invalid standalone bytes are
// retained rather than guessed to be a split rune.
func splitUTF8Prefix(context, tail []byte) int {
	if len(context) == 0 || len(tail) == 0 || tail[0] < 0x80 || tail[0] > 0xbf {
		return 0
	}

	window := make([]byte, 0, len(context)+utf8.UTFMax)
	window = append(window, context...)
	prefix := len(tail)
	if prefix > utf8.UTFMax {
		prefix = utf8.UTFMax
	}
	window = append(window, tail[:prefix]...)
	boundary := len(context)

	start := boundary - utf8ContextBytes
	if start < 0 {
		start = 0
	}
	for i := start; i < boundary; i++ {
		width := utf8SequenceLength(window[i])
		if width < 2 || i+width <= boundary || i+width > len(window) {
			continue
		}
		if utf8.Valid(window[i : i+width]) {
			return i + width - boundary
		}
	}
	return 0
}

type byteRing struct {
	buf    []byte
	start  int
	length int
}

func newByteRing(limit int) byteRing {
	if limit <= 0 {
		return byteRing{}
	}
	return byteRing{buf: make([]byte, limit)}
}

func (r *byteRing) write(p []byte) {
	limit := len(r.buf)
	if limit == 0 || len(p) == 0 {
		return
	}
	if len(p) >= limit {
		copy(r.buf, p[len(p)-limit:])
		r.start = 0
		r.length = limit
		return
	}

	if r.length < limit {
		add := limit - r.length
		if add > len(p) {
			add = len(p)
		}
		r.copyAt((r.start+r.length)%limit, p[:add])
		r.length += add
		p = p[add:]
	}
	if len(p) == 0 {
		return
	}

	r.copyAt(r.start, p)
	r.start = (r.start + len(p)) % limit
}

func (r *byteRing) copyAt(index int, p []byte) {
	first := copy(r.buf[index:], p)
	copy(r.buf, p[first:])
}

func (r *byteRing) bytes() []byte {
	if r.length == 0 {
		return nil
	}
	result := make([]byte, r.length)
	firstLength := r.length
	if available := len(r.buf) - r.start; firstLength > available {
		firstLength = available
	}
	copy(result, r.buf[r.start:r.start+firstLength])
	if firstLength < r.length {
		copy(result[firstLength:], r.buf[:r.length-firstLength])
	}
	return result
}
