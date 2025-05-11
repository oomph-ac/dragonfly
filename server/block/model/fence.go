package model

import (
	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/world"
)

// Fence is a model used by fences of any type. It can attach to blocks with solid faces and other fences of the same
// type and has a model height just slightly over 1.
type Fence struct {
	// Wood specifies if the Fence is made from wood. This field is used to check if two fences are able to attach to
	// each other.
	Wood bool
}

// BBox returns multiple physics.BBox depending on how many connections it has with the surrounding blocks.
func (f Fence) BBox(pos cube.Pos, s world.BlockSource) []cube.BBox {
	const inset = 0.375
	var boxes = make([]cube.BBox, 0, 5)

	connectWest, connectEast := f.checkFenceConnection(pos, cube.FaceWest, s), f.checkFenceConnection(pos, cube.FaceEast, s)
	connectNorth, connectSouth := f.checkFenceConnection(pos, cube.FaceNorth, s), f.checkFenceConnection(pos, cube.FaceSouth, s)

	// Check if we have any connections on the X axis (west/east)
	if connectWest || connectEast {
		sideBox := cube.Box(0, 0, 0, 1, 1.5, 1).Stretch(cube.Z, -inset)
		if connectWest {
			sideBox = sideBox.ExtendTowards(cube.FaceEast, -inset)
		}
		if connectEast {
			sideBox = sideBox.ExtendTowards(cube.FaceWest, -inset)
		}
		boxes = append(boxes, sideBox)
	}

	// Check if we have any connections on the Z axis (north/south)
	if connectNorth || connectSouth {
		sideBox := cube.Box(0, 0, 0, 1, 1.5, 1).Stretch(cube.X, -inset)
		if connectNorth {
			sideBox = sideBox.ExtendTowards(cube.FaceSouth, -inset)
		}
		if connectSouth {
			sideBox = sideBox.ExtendTowards(cube.FaceNorth, -inset)
		}
		boxes = append(boxes, sideBox)
	}

	// If no connections, create a center post box
	if len(boxes) == 0 {
		boxes = append(boxes, cube.Box(inset, 0, inset, 1-inset, 1.5, 1-inset))
	}
	return boxes
}

// FaceSolid returns true if the face is cube.FaceDown or cube.FaceUp.
func (f Fence) FaceSolid(_ cube.Pos, face cube.Face, _ world.BlockSource) bool {
	return face == cube.FaceDown || face == cube.FaceUp
}

func (f Fence) checkFenceConnection(pos cube.Pos, face cube.Face, s world.BlockSource) bool {
	pos = pos.Side(face)
	sideBlock := s.Block(pos)
	if fence, ok := sideBlock.Model().(Fence); ok && fence.Wood == f.Wood || (sideBlock.Model().FaceSolid(pos, face, s)) {
		return true
	} else if _, ok := sideBlock.Model().(FenceGate); ok {
		return true
	}
	return false
}
