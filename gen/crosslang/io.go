package main

import (
	"bytes"
	"encoding/binary"
	"math"
)

// reader reads a packet body with a stack of windows: a length-prefixed
// value must be read to exactly its end, and nothing outside it.
type reader struct {
	buf    []byte
	pos    int
	limits []int
}

func newReader(b []byte) *reader { return &reader{buf: b, limits: []int{len(b)}} }

func (r *reader) limit() int { return r.limits[len(r.limits)-1] }

func (r *reader) pushLimit(n int) error {
	end := r.pos + n
	if end > r.limit() {
		return wireErr("length-prefixed window of %d runs past the end", n)
	}
	r.limits = append(r.limits, end)
	return nil
}

func (r *reader) popLimit() error {
	end := r.limits[len(r.limits)-1]
	r.limits = r.limits[:len(r.limits)-1]
	if r.pos != end {
		return wireErr("length-prefixed window: %d bytes left unread", end-r.pos)
	}
	return nil
}

func (r *reader) take(n int) ([]byte, error) {
	if n < 0 {
		return nil, wireErr("negative length %d", n)
	}
	if r.pos+n > r.limit() {
		return nil, wireErr("want %d bytes, %d left", n, r.limit()-r.pos)
	}
	b := r.buf[r.pos : r.pos+n]
	r.pos += n
	return b, nil
}

func (r *reader) u8() (byte, error) {
	b, err := r.take(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (r *reader) rest() []byte {
	b := r.buf[r.pos:r.limit()]
	r.pos = r.limit()
	return b
}

func (r *reader) left() int { return r.limit() - r.pos }

// writer collects the re-encoding.
type writer struct{ bytes.Buffer }

func (w *writer) raw(b []byte) { w.Write(b) }

func (w *writer) u8(b byte) { w.WriteByte(b) }

func (w *writer) be16(v uint16) {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	w.Write(b[:])
}

func (w *writer) be32(v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	w.Write(b[:])
}

func (w *writer) be64(v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	w.Write(b[:])
}

func (w *writer) f32(v float32) { w.be32(math.Float32bits(v)) }
func (w *writer) f64(v float64) { w.be64(math.Float64bits(v)) }
