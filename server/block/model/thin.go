package model

import (
	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/world"
)

// Thin is a model for thin, partial blocks such as a glass pane or an iron bar. It changes its bounding box depending
// on solid faces next to it.
type Thin struct{}

// BBox returns a slice of physics.BBox that depends on the blocks surrounding the Thin block. Thin blocks can connect
// to any other Thin block, wall or solid faces of other blocks.
func (t Thin) BBox(pos cube.Pos, s world.BlockSource) (bbs []cube.BBox) {
	const inset = float64(7.0 / 16.0)
	connectWest, connectEast := t.checkConnection(pos, cube.FaceWest, s), t.checkConnection(pos, cube.FaceEast, s)
	if connectWest || connectEast {
		bb := cube.Box(0, 0, 0, 1, 1, 1).Stretch(cube.Z, -inset)
		if !connectWest {
			bb = bb.ExtendTowards(cube.FaceWest, -inset)
		} else if !connectEast {
			bb = bb.ExtendTowards(cube.FaceEast, -inset)
		}
		bbs = append(bbs, bb)
	}

	connectNorth, connectSouth := t.checkConnection(pos, cube.FaceNorth, s), t.checkConnection(pos, cube.FaceSouth, s)
	if connectNorth || connectSouth {
		bb := cube.Box(0, 0, 0, 1, 1, 1).Stretch(cube.X, -inset)
		if !connectNorth {
			bb = bb.ExtendTowards(cube.FaceNorth, -inset)
		} else if !connectSouth {
			bb = bb.ExtendTowards(cube.FaceSouth, -inset)
		}
		bbs = append(bbs, bb)
	}

	// This will happen if there are no connections in any direction.
	if len(bbs) == 0 {
		bbs = append(bbs, cube.Box(0, 0, 0, 1, 1, 1).Stretch(cube.X, -inset).Stretch(cube.Z, -inset))
	}
	return
}

// FaceSolid returns true if the face passed is cube.FaceDown.
func (t Thin) FaceSolid(_ cube.Pos, face cube.Face, _ world.BlockSource) bool {
	return face == cube.FaceDown
}

func (t Thin) checkConnection(pos cube.Pos, face cube.Face, s world.BlockSource) bool {
	sidePos := pos.Side(face)
	sideBlock := s.Block(sidePos)
	_, isThin := sideBlock.Model().(Thin)
	_, isWall := sideBlock.Model().(Wall)
	return isThin || isWall || sideBlock.Model().FaceSolid(sidePos, face, s)
}
