package chunk

import (
	"bytes"
	"testing"

	"github.com/df-mc/dragonfly/server/block/cube"
)

func TestChunkConvertsBlockNetworkHashesToRuntimeIDs(t *testing.T) {
	registry := networkHashTestRegistry{air: 0, hashToRuntimeID: map[uint32]uint32{100: 1}}
	c := New(registry, cube.Range{0, 15})
	c.SetBlock(1, 2, 3, 0, 100)

	c.ConvertBlockNetworkHashesToRuntimeIDs()

	if got := c.Block(1, 2, 3, 0); got != 1 {
		t.Fatalf("block runtime ID = %d, want 1", got)
	}
}

func TestEncodeWithBlockNetworkHashesDoesNotMutateChunk(t *testing.T) {
	registry := networkHashTestRegistry{air: 0, runtimeIDToHash: map[uint32]uint32{1: 100}}
	c := New(registry, cube.Range{0, 15})
	c.SetBlock(1, 2, 3, 0, 1)

	data := EncodeWithBlockNetworkHashes(c)
	buf := bytes.NewBuffer(data.SubChunks[0])
	index := byte(0)
	decoded, err := decodeSubChunk(buf, c, &index, NetworkEncoding)
	if err != nil {
		t.Fatal(err)
	}
	if got := decoded.Block(1, 2, 3, 0); got != 100 {
		t.Fatalf("encoded block ID = %d, want network hash 100", got)
	}
	if got := c.Block(1, 2, 3, 0); got != 1 {
		t.Fatalf("source block ID = %d after encoding, want 1", got)
	}
}

func TestEncodeSubChunkWithBlockNetworkHashesDoesNotMutateChunk(t *testing.T) {
	registry := networkHashTestRegistry{runtimeIDToHash: map[uint32]uint32{7: 70}}
	c := New(registry, cube.Range{0, 15})
	c.SetBlock(1, 1, 1, 0, 7)

	encoded := EncodeSubChunkWithBlockNetworkHashes(c, 0)
	buf := bytes.NewBuffer(encoded)
	index := byte(0)
	decoded, err := decodeSubChunk(buf, c, &index, NetworkEncoding)
	if err != nil {
		t.Fatal(err)
	}
	if got := decoded.Block(1, 1, 1, 0); got != 70 {
		t.Fatalf("encoded block runtime ID = %d, want network hash 70", got)
	}
	if got := c.Block(1, 1, 1, 0); got != 7 {
		t.Fatalf("source block runtime ID = %d, want 7", got)
	}
}

type networkHashTestRegistry struct {
	air             uint32
	hashToRuntimeID map[uint32]uint32
	runtimeIDToHash map[uint32]uint32
}

func (r networkHashTestRegistry) BlockCount() int      { return 1000 }
func (r networkHashTestRegistry) AirRuntimeID() uint32 { return r.air }
func (networkHashTestRegistry) RuntimeIDToState(uint32) (string, map[string]any, bool) {
	return "test:block", nil, true
}
func (networkHashTestRegistry) StateToRuntimeID(string, map[string]any) (uint32, bool) {
	return 0, true
}
func (networkHashTestRegistry) FilteringBlock(uint32) uint8       { return 0 }
func (networkHashTestRegistry) LightBlock(uint32) uint8           { return 0 }
func (networkHashTestRegistry) RandomTickBlock(uint32) bool       { return false }
func (networkHashTestRegistry) NBTBlock(uint32) bool              { return false }
func (networkHashTestRegistry) LiquidDisplacingBlock(uint32) bool { return false }
func (networkHashTestRegistry) LiquidBlock(uint32) bool           { return false }
func (r networkHashTestRegistry) HashToRuntimeID(hash uint32) (uint32, bool) {
	runtimeID, ok := r.hashToRuntimeID[hash]
	return runtimeID, ok
}
func (r networkHashTestRegistry) RuntimeIDToHash(runtimeID uint32) (uint32, bool) {
	hash, ok := r.runtimeIDToHash[runtimeID]
	return hash, ok
}
