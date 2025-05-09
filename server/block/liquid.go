package block

import (
	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/event"
	"github.com/df-mc/dragonfly/server/item"
	"github.com/df-mc/dragonfly/server/world"
	"math"
	"sync"
)

// LiquidRemovable represents a block that may be removed by a liquid flowing into it. When this happens, the
// block's drops are dropped at the position if HasLiquidDrops returns true.
type LiquidRemovable interface {
	HasLiquidDrops() bool
}

// sourceWaterDisplacer may be embedded to allow displacing water source blocks.
type sourceWaterDisplacer struct{}

// CanDisplace returns true if the world.Liquid passed is of the type Water, not falling and has a depth of 8.
func (s sourceWaterDisplacer) CanDisplace(b world.Liquid) bool {
	w, ok := b.(Water)
	return ok && !w.Falling && w.Depth == 8
}

// flowingWaterDisplacer may be embedded to allow displacing water source blocks or flowing water.
type flowingWaterDisplacer struct{}

// CanDisplace returns true if the world.Liquid passed is of the type Water.
func (s flowingWaterDisplacer) CanDisplace(b world.Liquid) bool {
	_, ok := b.(Water)
	return ok
}

// tickLiquid ticks the liquid block passed at a specific position in the world. Depending on the surroundings
// and the liquid block, the liquid will either spread or decrease in depth. Additionally, the liquid might
// be turned into a solid block if a different liquid is next to it.
func tickLiquid(b world.Liquid, pos cube.Pos, tx *world.Tx) {
	if !source(b) && !sourceAround(b, pos, tx) {
		var res world.Liquid
		if b.LiquidDepth()-4 > 0 {
			res = b.WithDepth(b.LiquidDepth()-2*b.SpreadDecay(), false)
		}
		ctx := event.C(tx)
		if tx.World().Handler().HandleLiquidDecay(ctx, pos, b, res); ctx.Cancelled() {
			return
		}
		tx.SetLiquid(pos, res)
		return
	}
	displacer, _ := tx.Block(pos).(world.LiquidDisplacer)

	canFlowBelow := canFlowInto(b, tx, pos.Side(cube.FaceDown), false)
	if b.LiquidFalling() && !canFlowBelow {
		b = b.WithDepth(8, true)
	} else if canFlowBelow {
		below := pos.Side(cube.FaceDown)
		if displacer == nil || !displacer.SideClosed(pos, below, tx) {
			flowInto(b.WithDepth(8, true), pos, below, tx, true)
		}
	}

	depth, decay := b.LiquidDepth(), b.SpreadDecay()
	if depth <= decay {
		// Current depth is smaller than the decay, so spreading will result in nothing.
		return
	}
	if source(b) || !canFlowBelow {
		paths := calculateLiquidPaths(b, pos, tx, displacer)
		if len(paths) == 0 {
			spreadOutwards(b, pos, tx, displacer)
			return
		}

		smallestLen := len(paths[0])
		for _, path := range paths {
			if len(path) <= smallestLen {
				flowInto(b, pos, path[0], tx, false)
			}
		}
	}
}

// source checks if a liquid is a source block.
func source(b world.Liquid) bool {
	return b.LiquidDepth() == 8 && !b.LiquidFalling()
}

// spreadOutwards spreads the liquid outwards into the horizontal directions.
func spreadOutwards(b world.Liquid, pos cube.Pos, tx *world.Tx, displacer world.LiquidDisplacer) {
	pos.Neighbours(func(neighbour cube.Pos) {
		if neighbour[1] == pos[1] {
			if displacer == nil || !displacer.SideClosed(pos, neighbour, tx) {
				flowInto(b, pos, neighbour, tx, false)
			}
		}
	}, tx.Range())
}

// sourceAround checks if there is a source in the blocks around the position passed.
func sourceAround(b world.Liquid, pos cube.Pos, tx *world.Tx) (sourcePresent bool) {
	pos.Neighbours(func(neighbour cube.Pos) {
		if neighbour[1] == pos[1]-1 {
			// We don't care about water below this one.
			return
		}
		side, ok := tx.Liquid(neighbour)
		if !ok || side.LiquidType() != b.LiquidType() {
			return
		}
		if displacer, ok := tx.Block(neighbour).(world.LiquidDisplacer); ok && displacer.SideClosed(neighbour, pos, tx) {
			// The side towards this liquid was closed, so this cannot function as a source for this
			// liquid.
			return
		}
		if neighbour[1] == pos[1]+1 || source(side) || side.LiquidDepth() > b.LiquidDepth() {
			sourcePresent = true
		}
	}, tx.Range())
	return
}

// flowInto makes the liquid passed flow into the position passed in a world. If successful, the block at that
// position will be broken and the liquid with a lower depth will replace it.
func flowInto(b world.Liquid, src, pos cube.Pos, tx *world.Tx, falling bool) bool {
	newDepth := b.LiquidDepth() - b.SpreadDecay()
	if falling {
		newDepth = b.LiquidDepth()
	}
	if newDepth <= 0 && !falling {
		return false
	}
	existing := tx.Block(pos)
	if existingLiquid, alsoLiquid := existing.(world.Liquid); alsoLiquid && existingLiquid.LiquidType() == b.LiquidType() {
		if existingLiquid.LiquidDepth() >= newDepth || existingLiquid.LiquidFalling() {
			// The existing liquid had a higher depth than the one we're propagating, or it was falling
			// (basically considered full depth), so no need to continue.
			return true
		}
		ctx := event.C(tx)
		if tx.World().Handler().HandleLiquidFlow(ctx, src, pos, b.WithDepth(newDepth, falling), existing); ctx.Cancelled() {
			return false
		}
		tx.SetLiquid(pos, b.WithDepth(newDepth, falling))
		return true
	} else if alsoLiquid {
		existingLiquid.Harden(pos, tx, &src)
		return false
	}
	displacer, isDisplacer := existing.(world.LiquidDisplacer)
	if isDisplacer {
		if _, ok := tx.Liquid(pos); ok {
			// We've got a liquid displacer, and it's got a liquid within it, so we can't flow into this.
			return false
		}
	}
	removable, isRemovable := existing.(LiquidRemovable)
	if !isRemovable && (!isDisplacer || !displacer.CanDisplace(b.WithDepth(newDepth, falling))) {
		// Can't flow into this block.
		return false
	}
	ctx := event.C(tx)
	if tx.World().Handler().HandleLiquidFlow(ctx, src, pos, b.WithDepth(newDepth, falling), existing); ctx.Cancelled() {
		return false
	}

	if isRemovable {
		if _, air := existing.(Air); !air {
			tx.SetBlock(pos, nil, nil)
		}
		if removable.HasLiquidDrops() {
			if b, ok := existing.(Breakable); ok {
				for _, d := range b.BreakInfo().Drops(item.ToolNone{}, nil) {
					dropItem(tx, d, pos.Vec3Centre())
				}
			} else {
				panic("liquid drops should always implement breakable")
			}
		}
	}
	tx.SetLiquid(pos, b.WithDepth(newDepth, falling))
	return true
}

// liquidPath represents a path to an empty lower block or a block that can be flown into by a liquid, which
// the liquid tends to flow into. All paths with the lowest length will be filled with water.
type liquidPath []cube.Pos

// calculateLiquidPaths calculates paths in the world that the liquid passed can flow in to reach lower
// grounds, starting at the position passed.
// If none of these paths can be found, the returned slice has a length of 0.
func calculateLiquidPaths(b world.Liquid, pos cube.Pos, tx *world.Tx, displacer world.LiquidDisplacer) []liquidPath {
	queue := liquidQueuePool.Get().(*liquidQueue)
	defer func() {
		queue.Reset()
		liquidQueuePool.Put(queue)
	}()
	queue.PushBack(liquidNode{x: pos[0], z: pos[2], depth: int8(b.LiquidDepth())})
	decay := int8(b.SpreadDecay())

	paths := make([]liquidPath, 0, 3)
	first := true

	for {
		if queue.Len() == 0 {
			break
		}
		node := queue.Front()
		neighA, neighB, neighC, neighD := node.neighbours(decay * 2)
		if !first || (displacer == nil || !displacer.SideClosed(pos, cube.Pos{neighA.x, pos[1], neighA.z}, tx)) {
			if spreadNeighbour(b, pos, tx, neighA, queue) {
				queue.shortestPath = neighA.Len()
				paths = append(paths, neighA.Path(pos))
			}
		}
		if !first || (displacer == nil || !displacer.SideClosed(pos, cube.Pos{neighB.x, pos[1], neighB.z}, tx)) {
			if spreadNeighbour(b, pos, tx, neighB, queue) {
				queue.shortestPath = neighB.Len()
				paths = append(paths, neighB.Path(pos))
			}
		}
		if !first || (displacer == nil || !displacer.SideClosed(pos, cube.Pos{neighC.x, pos[1], neighC.z}, tx)) {
			if spreadNeighbour(b, pos, tx, neighC, queue) {
				queue.shortestPath = neighC.Len()
				paths = append(paths, neighC.Path(pos))
			}
		}
		if !first || (displacer == nil || !displacer.SideClosed(pos, cube.Pos{neighD.x, pos[1], neighD.z}, tx)) {
			if spreadNeighbour(b, pos, tx, neighD, queue) {
				queue.shortestPath = neighD.Len()
				paths = append(paths, neighD.Path(pos))
			}
		}
		first = false
	}
	return paths
}

// spreadNeighbour attempts to spread a path node into the neighbour passed. Note that this does not spread
// the liquid, it only spreads the node used to calculate flow paths.
func spreadNeighbour(b world.Liquid, src cube.Pos, tx *world.Tx, node liquidNode, queue *liquidQueue) bool {
	if node.depth+3 <= 0 {
		// Depth has reached zero or below, can't spread any further.
		return false
	}
	if node.Len() > queue.shortestPath {
		// This path is longer than any existing path, so don't spread any further.
		return false
	}
	pos := cube.Pos{node.x, src[1], node.z}
	if !canFlowInto(b, tx, pos, true) {
		// Can't flow into this block, can't spread any further.
		return false
	}
	pos[1]--
	if canFlowInto(b, tx, pos, false) {
		return true
	}
	queue.PushBack(node)
	return false
}

// canFlowInto checks if a liquid can flow into the block present in the world at a specific block position.
func canFlowInto(b world.Liquid, tx *world.Tx, pos cube.Pos, sideways bool) bool {
	bl := tx.Block(pos)
	if _, air := bl.(Air); air {
		// Fast route for air: A type assert to a concrete type is much faster than a type assert to an interface.
		return true
	}
	if _, ok := bl.(LiquidRemovable); ok {
		if liq, ok := bl.(world.Liquid); ok && sideways {
			if (liq.LiquidDepth() == 8 && !liq.LiquidFalling()) || liq.LiquidType() != b.LiquidType() {
				// Can't flow into a liquid if it has a depth of 8 or if it doesn't have the same type.
				return false
			}
		}
		return true
	}
	if dis, ok := bl.(world.LiquidDisplacer); ok {
		res := b.WithDepth(b.LiquidDepth()-b.SpreadDecay(), !sideways)
		if dis.CanDisplace(res) {
			return true
		}
	}
	return false
}

// liquidNode represents a position that is part of a flow path for a liquid.
type liquidNode struct {
	x, z     int
	depth    int8
	previous *liquidNode
}

// neighbours returns the four horizontal neighbours of the node with decreased depth.
func (node liquidNode) neighbours(decay int8) (a, b, c, d liquidNode) {
	return liquidNode{x: node.x - 1, z: node.z, depth: node.depth - decay, previous: &node},
		liquidNode{x: node.x + 1, z: node.z, depth: node.depth - decay, previous: &node},
		liquidNode{x: node.x, z: node.z - 1, depth: node.depth - decay, previous: &node},
		liquidNode{x: node.x, z: node.z + 1, depth: node.depth - decay, previous: &node}
}

// Len returns the length of the path created by the node.
func (node liquidNode) Len() int {
	i := 1
	for {
		if node.previous == nil {
			return i - 1
		}
		//noinspection GoAssignmentToReceiver
		node = *node.previous
		i++
	}
}

// Path converts the liquid node into a path.
func (node liquidNode) Path(src cube.Pos) liquidPath {
	l := node.Len()
	path := make(liquidPath, l)
	i := l - 1
	for {
		if node.previous == nil {
			return path
		}
		path[i] = cube.Pos{node.x, src[1], node.z}

		//noinspection GoAssignmentToReceiver
		node = *node.previous
		i--
	}
}

// liquidQueuePool is use to re-use liquid node queues.
var liquidQueuePool = sync.Pool{
	New: func() any {
		return &liquidQueue{
			nodes:        make([]liquidNode, 0, 64),
			shortestPath: math.MaxInt8,
		}
	},
}

// liquidQueue represents a queue that may be used to push nodes into and take them out of it.
type liquidQueue struct {
	nodes        []liquidNode
	i            int
	shortestPath int
}

func (q *liquidQueue) PushBack(node liquidNode) {
	q.nodes = append(q.nodes, node)
}

func (q *liquidQueue) Front() liquidNode {
	v := q.nodes[q.i]
	q.i++
	return v
}

func (q *liquidQueue) Len() int {
	return len(q.nodes) - q.i
}

func (q *liquidQueue) Reset() {
	q.nodes = q.nodes[:0]
	q.i = 0
	q.shortestPath = math.MaxInt8
}
