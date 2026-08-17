package capture

import (
	"bytes"
	"math/rand"
	"reflect"
	"testing"
)

func TestStreamCollectorKeepsMediumStreamWhole(t *testing.T) {
	input := bytes.Repeat([]byte("m"), 100<<10)
	collector := newStreamCollector(defaultHeadBytes, defaultTailBytes)
	writeCollector(t, collector, input)

	got := collector.snapshot()
	if !bytes.Equal(got.head, input) {
		t.Fatal("complete stream was not retained in head")
	}
	if got.omitted != 0 || len(got.tail) != 0 {
		t.Fatalf("omitted = %d, tail bytes = %d; want 0, 0", got.omitted, len(got.tail))
	}
}

func TestStreamCollectorBoundaries(t *testing.T) {
	headLimit := 4
	tailLimit := 6
	budget := headLimit + tailLimit
	for _, size := range []int{0, 1, budget - 1, budget, budget + 1} {
		t.Run(string(rune('A'+size)), func(t *testing.T) {
			input := makeSequence(size)
			collector := newStreamCollector(headLimit, tailLimit)
			writeCollector(t, collector, input)
			got := collector.snapshot()

			if got.received != int64(size) {
				t.Fatalf("received = %d, want %d", got.received, size)
			}
			if size <= budget {
				if !bytes.Equal(got.head, input) || got.omitted != 0 || len(got.tail) != 0 {
					t.Fatalf("snapshot = %#v, want complete head", got)
				}
				return
			}
			if !bytes.Equal(got.head, input[:headLimit]) {
				t.Fatalf("head = %v, want %v", got.head, input[:headLimit])
			}
			if !bytes.Equal(got.tail, input[len(input)-tailLimit:]) {
				t.Fatalf("tail = %v, want %v", got.tail, input[len(input)-tailLimit:])
			}
			if got.omitted != 1 {
				t.Fatalf("omitted = %d, want 1", got.omitted)
			}
		})
	}
}

func TestStreamCollectorIsIndependentOfWriteChunks(t *testing.T) {
	input := makeSequence(defaultHeadBytes + defaultTailBytes + 12345)
	input[defaultHeadBytes-1] = 0xff

	wantCollector := newStreamCollector(defaultHeadBytes, defaultTailBytes)
	writeCollector(t, wantCollector, input)
	want := wantCollector.snapshot()

	bytewise := newStreamCollector(defaultHeadBytes, defaultTailBytes)
	for _, value := range input {
		writeCollector(t, bytewise, []byte{value})
	}
	if got := bytewise.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("byte-wise snapshot differs from single-write snapshot")
	}

	random := newStreamCollector(defaultHeadBytes, defaultTailBytes)
	rng := rand.New(rand.NewSource(17))
	for remaining := input; len(remaining) > 0; {
		size := rng.Intn(4096) + 1
		if size > len(remaining) {
			size = len(remaining)
		}
		writeCollector(t, random, remaining[:size])
		remaining = remaining[size:]
	}
	if got := random.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("random-chunk snapshot differs from single-write snapshot")
	}
}

func TestStreamCollectorMovesSplitUTF8RunesIntoOmission(t *testing.T) {
	headLimit := 8
	tailLimit := 12
	input := bytes.Repeat([]byte("a"), headLimit+tailLimit+10)

	copy(input[headLimit-1:], []byte("€"))
	tailStart := len(input) - tailLimit
	copy(input[tailStart-2:], []byte("€"))

	collector := newStreamCollector(headLimit, tailLimit)
	writeCollector(t, collector, input)
	got := collector.snapshot()

	if len(got.head) != headLimit-1 {
		t.Fatalf("head bytes = %d, want %d", len(got.head), headLimit-1)
	}
	if len(got.tail) != tailLimit-1 {
		t.Fatalf("tail bytes = %d, want %d", len(got.tail), tailLimit-1)
	}
	if got.omitted != 12 {
		t.Fatalf("omitted = %d, want 12", got.omitted)
	}
	if !bytes.Equal(got.head, input[:headLimit-1]) {
		t.Fatal("head is not the expected prefix")
	}
	if !bytes.Equal(got.tail, input[tailStart+1:]) {
		t.Fatal("tail is not the expected suffix after the split rune")
	}
}

func TestStreamCollectorPreservesUnprovenInvalidBoundaryByte(t *testing.T) {
	collector := newStreamCollector(1, 2)
	writeCollector(t, collector, []byte{'a', 'b', 0x80, 'c'})
	got := collector.snapshot()

	if !bytes.Equal(got.tail, []byte{0x80, 'c'}) {
		t.Fatalf("tail = %v, want standalone invalid byte retained", got.tail)
	}
	if got.omitted != 1 {
		t.Fatalf("omitted = %d, want 1", got.omitted)
	}
}

func TestStreamCollectorSnapshotIsNonDestructive(t *testing.T) {
	collector := newStreamCollector(2, 2)
	writeCollector(t, collector, []byte("abcde"))
	first := collector.snapshot()
	writeCollector(t, collector, []byte("f"))
	second := collector.snapshot()

	if first.received != 5 || !bytes.Equal(first.head, []byte("ab")) || !bytes.Equal(first.tail, []byte("de")) {
		t.Fatalf("first snapshot = %#v", first)
	}
	if second.received != 6 || !bytes.Equal(second.head, []byte("ab")) || !bytes.Equal(second.tail, []byte("ef")) {
		t.Fatalf("second snapshot = %#v", second)
	}
	if !bytes.Equal(first.tail, []byte("de")) {
		t.Fatal("later writes mutated the earlier snapshot")
	}
}

func TestStreamCollectorRetainedMemoryDoesNotGrowWithInput(t *testing.T) {
	collector := newStreamCollector(defaultHeadBytes, defaultTailBytes)
	chunk := bytes.Repeat([]byte("x"), 1<<20)
	for i := 0; i < 1024; i++ {
		writeCollector(t, collector, chunk)
	}

	if got, want := collector.received, int64(1)<<30; got != want {
		t.Fatalf("received = %d, want %d", got, want)
	}
	if got, max := collector.retainedCapacity(), defaultHeadBytes+defaultTailBytes+utf8ContextBytes; got > max {
		t.Fatalf("retained capacity = %d, want at most %d", got, max)
	}
	got := collector.snapshot()
	if got.omitted != got.received-int64(len(got.head))-int64(len(got.tail)) {
		t.Fatal("snapshot accounting is inconsistent")
	}
}

func FuzzStreamCollectorAccounting(f *testing.F) {
	f.Add([]byte(""), uint8(0), uint8(0))
	f.Add([]byte("hello"), uint8(2), uint8(2))
	f.Add([]byte{0xf0, 0x9f, 0x92, 0xa5, 0xff}, uint8(1), uint8(2))

	f.Fuzz(func(t *testing.T, input []byte, headByte, tailByte uint8) {
		headLimit := int(headByte % 32)
		tailLimit := int(tailByte % 32)
		collector := newStreamCollector(headLimit, tailLimit)
		writeCollector(t, collector, input)
		got := collector.snapshot()

		if got.received != int64(len(input)) {
			t.Fatalf("received = %d, want %d", got.received, len(input))
		}
		if int64(len(got.head))+got.omitted+int64(len(got.tail)) != got.received {
			t.Fatalf("accounting does not sum to received bytes: %#v", got)
		}
		if !bytes.Equal(got.head, input[:len(got.head)]) {
			t.Fatal("retained head is not an exact prefix")
		}
		if !bytes.Equal(got.tail, input[len(input)-len(got.tail):]) {
			t.Fatal("retained tail is not an exact suffix")
		}
		if got.omitted == 0 && (!bytes.Equal(got.head, input) || len(got.tail) != 0) {
			t.Fatal("untruncated stream was not represented entirely in head")
		}
	})
}

func writeCollector(t *testing.T, collector *streamCollector, value []byte) {
	t.Helper()
	written, err := collector.Write(value)
	if err != nil {
		t.Fatal(err)
	}
	if written != len(value) {
		t.Fatalf("Write returned %d, want %d", written, len(value))
	}
}

func makeSequence(size int) []byte {
	result := make([]byte, size)
	for i := range result {
		result[i] = byte(i % 251)
	}
	return result
}
