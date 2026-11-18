package taskcheck

import "bytes"

// tailBuffer is an io.Writer that keeps the last max bytes written to it and counts the bytes it dropped.
type tailBuffer struct {
	max     int
	buf     []byte
	dropped int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.dropped += over
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return n, nil
}

// String returns the kept text. After a cut, it starts at the next line start, and cut counts the bytes before it.
func (t *tailBuffer) String() (text string, cut int) {
	if t.dropped == 0 {
		return string(t.buf), 0
	}
	if i := bytes.IndexByte(t.buf, '\n'); i >= 0 {
		return string(t.buf[i+1:]), t.dropped + i + 1
	}
	return string(t.buf), t.dropped
}
