// Copyright 2014 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package base provides common utilities for other stenographer libraries.
package base

import (
	"container/heap"
	"flag"
	"log"
	"sort"
	"sync"

	"code.google.com/p/gopacket"
)

var verboseLogging = flag.Int("v", 0, "log many verbose logs")

// V provides verbose logging which can be turned on/off with the -v flag.
func V(level int, fmt string, args ...interface{}) {
	if *verboseLogging >= level {
		log.Printf(fmt, args...)
	}
}

// Packet is a single packet with its metadata.
type Packet struct {
	Data                 []byte // The actual bytes that make up the packet
	gopacket.CaptureInfo        // Metadata about when/how the packet was captured
}

// PacketChan provides an async method for passing multiple ordered packets
// between goroutines.
type PacketChan struct {
	mu  sync.Mutex
	c   chan *Packet
	err error
}

// Receive provides the channel from which to read packets.  It always
// returns the same channel.
func (p *PacketChan) Receive() <-chan *Packet { return p.c }

// Send sends a single packet on the channel to the receiver.
func (p *PacketChan) Send(pkt *Packet) { p.c <- pkt }

// Close closes the sending channel and sets the PacketChan's error based
// in its input.
func (p *PacketChan) Close(err error) {
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
	close(p.c)
}

// NewPacketChan returns a new PacketChan channel for passing packets around.
func NewPacketChan(buffer int) PacketChan {
	return PacketChan{
		c: make(chan *Packet, buffer),
	}
}

// Discard discards all remaining packets on the receiving end.  If you stop
// using the channel before reading all packets, you must call this function.
// It's a good idea to defer this regardless.
func (p *PacketChan) Discard() {
	go func() {
		discarded := 0
		for _ = range p.c {
			discarded++
		}
		if discarded > 0 {
			V(2, "discarded %v", discarded)
		}
	}()
}

// Err gets the current error for the channel, if any exists.  This may be
// called during Next(), but if an error occurs it may only be set after Next()
// returns false the first time.
func (p *PacketChan) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// indexedPacket is used internally by MergePacketChans.
type indexedPacket struct {
	*Packet
	i int
}

// packetHeap is used internally by MergePacketChans.
type packetHeap []indexedPacket

func (p packetHeap) Len() int            { return len(p) }
func (p packetHeap) Swap(i, j int)       { p[i], p[j] = p[j], p[i] }
func (p packetHeap) Less(i, j int) bool  { return p[i].Timestamp.Before(p[j].Timestamp) }
func (p *packetHeap) Push(x interface{}) { *p = append(*p, x.(indexedPacket)) }
func (p *packetHeap) Pop() (x interface{}) {
	index := len(*p) - 1
	*p, x = (*p)[:index], (*p)[index]
	return
}

// MergePacketChans merges an incoming set of packet chans, each sorted by
// time, returning a new single packet chan that's also sorted by time.
func MergePacketChans(in []PacketChan) PacketChan {
	out := NewPacketChan(100)
	go func() {
		count := 0
		defer func() {
			V(1, "merged %d streams for %d total packets", len(in), count)
		}()
		var h packetHeap
		for i := range in {
			defer in[i].Discard()
		}
		for i, c := range in {
			if pkt := <-c.Receive(); pkt != nil {
				heap.Push(&h, indexedPacket{Packet: pkt, i: i})
			}
			if err := c.Err(); err != nil {
				out.Close(err)
				return
			}
		}
		for h.Len() > 0 {
			p := heap.Pop(&h).(indexedPacket)
			count++
			if pkt := <-in[p.i].Receive(); pkt != nil {
				heap.Push(&h, indexedPacket{Packet: pkt, i: p.i})
			}
			out.c <- p.Packet
			if err := in[p.i].Err(); err != nil {
				out.Close(err)
				return
			}
		}
		out.Close(nil)
	}()
	return out
}

// Int64Slice is a simple method for sorting a slice of int64s, then doing
// simple set operations on those slices.
type Int64Slice []int64

func (a Int64Slice) Less(i, j int) bool {
	return a[i] < a[j]
}
func (a Int64Slice) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}
func (a Int64Slice) Len() int {
	return len(a)
}
func (a Int64Slice) Sort() {
	sort.Sort(a)
}

// Union returns the union of a and b.  a and b must be sorted in advance.
// Returned slice will be sorted.
func (a Int64Slice) Union(b Int64Slice) (out Int64Slice) {
	out = make(Int64Slice, 0, len(a)+len(b)/2)
	ib := 0
	for _, pos := range a {
		for ib < len(b) && b[ib] < pos {
			out = append(out, b[ib])
			ib++
		}
		if ib < len(b) && b[ib] == pos {
			ib++
		}
		out = append(out, pos)
	}
	out = append(out, b[ib:]...)
	return out
}

// Intersect returns the intersection of a and b.  a and b must be sorted in
// advance.  Returned slice will be sorted.
func (a Int64Slice) Intersect(b Int64Slice) (out Int64Slice) {
	out = make(Int64Slice, 0, len(a)/2)
	ib := 0
	for _, pos := range a {
		for ib < len(b) && b[ib] < pos {
			ib++
		}
		if ib < len(b) && b[ib] == pos {
			out = append(out, pos)
			ib++
		}
	}
	return out
}
