package server_test

import (
	"slices"
	"testing"
)

// posixCksum is the checksum cksum(1) prints, which ci/test-shard.sh uses to
// put a test in shard 1 or 2.
func posixCksum(b []byte) uint32 {
	var crc uint32
	feed := func(c byte) {
		crc ^= uint32(c) << 24
		for range 8 {
			if crc&0x80000000 != 0 {
				crc = crc<<1 ^ 0x04C11DB7
			} else {
				crc <<= 1
			}
		}
	}
	for _, c := range b {
		feed(c)
	}
	for n := len(b); n > 0; n >>= 8 {
		feed(byte(n))
	}
	return ^crc
}

func TestLayoutPartsRunInDifferentShards(t *testing.T) {
	t.Parallel()
	if got := posixCksum([]byte("TestRenderedLayout")) % 2; got != 1 {
		t.Fatalf("self-check of the cksum: TestRenderedLayout is in shard %d, CI put it in shard 2", got+1)
	}
	a := posixCksum([]byte("TestRenderedLayout")) % 2
	b := posixCksum([]byte("TestRenderedLayoutPart2")) % 2
	if a == b {
		t.Errorf("TestRenderedLayout and TestRenderedLayoutPart2 are both in shard %d: one shard runs the whole layout check again; rename part 2", a+1)
	}
}

func TestLayoutPartsCoverEveryPageOnce(t *testing.T) {
	t.Parallel()
	pages := []int{0, 1, 2, 3, 4, 5, 6}
	got := append(layoutPart(pages, 0), layoutPart(pages, 1)...)
	slices.Sort(got)
	if !slices.Equal(got, pages) {
		t.Errorf("the two parts = %v, want every page once", got)
	}
}
