package model

import (
	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/world"
	"github.com/go-gl/mathgl/mgl64"
)

// Stair is a model for stair-like blocks. These have different solid sides depending on the direction the
// stairs are facing, the surrounding blocks and whether it is upside down or not.
type Stair struct {
	// Facing specifies the direction that the full side of the Stair faces.
	Facing cube.Direction
	// UpsideDown turns the Stair upside-down, meaning the full side of the Stair is turned to the top side of the
	// block.
	UpsideDown bool
}

// BBox returns a slice of physics.BBox depending on if the Stair is upside down and which direction it is facing.
// Additionally, these BBoxs depend on the Stair blocks surrounding this one, which can influence the model.
func (s Stair) BBox(pos cube.Pos, bs world.BlockSource) []cube.BBox {
	b := []cube.BBox{cube.Box(0, 0, 0, 1, 0.5, 1)}
	if s.UpsideDown {
		b[0] = cube.Box(0, 0.5, 0, 1, 1, 1)
	}
	t := s.cornerType(pos, bs)

	face, oppositeFace := s.Facing.Face(), s.Facing.Opposite().Face()
	if t == noCorner || t == cornerRightInner || t == cornerLeftInner {
		b = append(b, cube.Box(0.5, 0.5, 0.5, 0.5, 1, 0.5).
			ExtendTowards(face, 0.5).
			Stretch(s.Facing.RotateRight().Face().Axis(), 0.5))
	}
	if t == cornerRightOuter {
		b = append(b, cube.Box(0.5, 0.5, 0.5, 0.5, 1, 0.5).
			ExtendTowards(face, 0.5).
			ExtendTowards(s.Facing.RotateLeft().Face(), 0.5))
	} else if t == cornerLeftOuter {
		b = append(b, cube.Box(0.5, 0.5, 0.5, 0.5, 1, 0.5).
			ExtendTowards(face, 0.5).
			ExtendTowards(s.Facing.RotateRight().Face(), 0.5))
	} else if t == cornerRightInner {
		b = append(b, cube.Box(0.5, 0.5, 0.5, 0.5, 1, 0.5).
			ExtendTowards(oppositeFace, 0.5).
			ExtendTowards(s.Facing.RotateRight().Face(), 0.5))
	} else if t == cornerLeftInner {
		b = append(b, cube.Box(0.5, 0.5, 0.5, 0.5, 1, 0.5).
			ExtendTowards(oppositeFace, 0.5).
			ExtendTowards(s.Facing.RotateLeft().Face(), 0.5))
	}
	if s.UpsideDown {
		for i := range b[1:] {
			b[i+1] = b[i+1].Translate(mgl64.Vec3{0, -0.5})
		}
	}
	return b
}

// FaceSolid returns true for all faces of the Stair that are completely filled.
func (s Stair) FaceSolid(pos cube.Pos, face cube.Face, bs world.BlockSource) bool {
	if face == cube.FaceUp && s.UpsideDown ||
		face == cube.FaceDown && !s.UpsideDown {
		return true
	}

	t := s.cornerType(pos, bs)
	return (face == s.Facing.Face().Opposite() && t != cornerRightOuter && t != cornerLeftOuter) ||
		(face == s.Facing.RotateRight().Face() && t == cornerRightInner) ||
		(face == s.Facing.RotateLeft().Face() && t == cornerLeftInner)
}

const (
	noCorner = iota
	cornerRightInner
	cornerLeftInner
	cornerRightOuter
	cornerLeftOuter
)

// cornerType returns the type of the corner that the stairs form, or 0 if it does not form a corner with any
// other stairs.
func (s Stair) cornerType(pos cube.Pos, bs world.BlockSource) uint8 {
	rotatedFacing := s.Facing.RotateRight()
	if closedSide, ok := bs.Block(pos.Side(s.Facing.Face())).Model().(Stair); ok && closedSide.UpsideDown == s.UpsideDown {
		if closedSide.Facing == rotatedFacing {
			return cornerLeftOuter
		} else if closedSide.Facing == rotatedFacing.Opposite() {
			// This will only form a corner if there is not a stair on the right of this one with the same
			// direction.
			if side, ok := bs.Block(pos.Side(s.Facing.RotateRight().Face())).Model().(Stair); !ok || side.Facing != s.Facing || side.UpsideDown != s.UpsideDown {
				return cornerRightOuter
			}
			return noCorner
		}
	}
	if openSide, ok := bs.Block(pos.Side(s.Facing.Opposite().Face())).Model().(Stair); ok && openSide.UpsideDown == s.UpsideDown {
		if openSide.Facing == rotatedFacing {
			// This will only form a corner if there is not a stair on the right of this one with the same
			// direction.
			if side, ok := bs.Block(pos.Side(s.Facing.RotateRight().Face())).Model().(Stair); !ok || side.Facing != s.Facing || side.UpsideDown != s.UpsideDown {
				return cornerRightInner
			}
		} else if openSide.Facing == rotatedFacing.Opposite() {
			return cornerLeftInner
		}
	}
	return noCorner
}
