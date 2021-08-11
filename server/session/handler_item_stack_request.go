package session

import (
	"fmt"
	"github.com/df-mc/dragonfly/server/block"
	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/entity/effect"
	"github.com/df-mc/dragonfly/server/item"
	"github.com/df-mc/dragonfly/server/item/creative"
	"github.com/df-mc/dragonfly/server/world/recipes"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
	"math"
	"time"
)

// ItemStackRequestHandler handles the ItemStackRequest packet. It handles the actions done within the
// inventory.
type ItemStackRequestHandler struct {
	currentRequest  int32
	changes         map[byte]map[byte]protocol.StackResponseSlotInfo
	responseChanges map[int32]map[byte]map[byte]responseChange
	current         time.Time
	ignoreDestroy   bool
}

// responseChange represents a change in a specific item stack response. It holds the timestamp of the
// response which is used to get rid of changes that the client will have received.
type responseChange struct {
	id        int32
	timestamp time.Time
}

// Handle ...
func (h *ItemStackRequestHandler) Handle(p packet.Packet, s *Session) error {
	pk := p.(*packet.ItemStackRequest)
	h.current = time.Now()

	s.inTransaction.Store(true)
	defer s.inTransaction.Store(false)

	for _, req := range pk.Requests {
		h.currentRequest = req.RequestID
		if err := h.handleRequest(req, s); err != nil {
			// Item stacks being out of sync isn't uncommon, so don't error. Just debug the error and let the
			// revert do its work.
			s.log.Debugf("failed processing packet from %v (%v): ItemStackRequest: error resolving item stack request: %v", s.conn.RemoteAddr(), s.c.Name(), err)
		}
	}
	return nil
}

// handleRequest resolves a single item stack request from the client.
func (h *ItemStackRequestHandler) handleRequest(req protocol.ItemStackRequest, s *Session) (err error) {
	defer func() {
		if err != nil {
			h.reject(req.RequestID, s)
			return
		}
		h.resolve(req.RequestID, s)
		h.ignoreDestroy = false
	}()

	for _, action := range req.Actions {
		switch a := action.(type) {
		case *protocol.CraftRecipeStackRequestAction:
			err = h.handleCraft(a, s)
		case *protocol.TakeStackRequestAction:
			err = h.handleTake(a, s)
		case *protocol.PlaceStackRequestAction:
			err = h.handlePlace(a, s)
		case *protocol.SwapStackRequestAction:
			err = h.handleSwap(a, s)
		case *protocol.DestroyStackRequestAction:
			err = h.handleDestroy(a, s)
		case *protocol.DropStackRequestAction:
			err = h.handleDrop(a, s)
		case *protocol.BeaconPaymentStackRequestAction:
			err = h.handleBeaconPayment(a, s)
		case *protocol.CraftCreativeStackRequestAction:
			err = h.handleCreativeCraft(a, s)
		case *protocol.ConsumeStackRequestAction, *protocol.CraftResultsDeprecatedStackRequestAction, *protocol.MineBlockStackRequestAction:
			// Don't do anything with this.
		default:
			return fmt.Errorf("unhandled stack request action %#v", action)
		}
		if err != nil {
			err = fmt.Errorf("%T: %w", action, err)
			return
		}
	}
	return
}

// handleTake handles a Take stack request action.
func (h *ItemStackRequestHandler) handleTake(a *protocol.TakeStackRequestAction, s *Session) error {
	return h.handleTransfer(a.Source, a.Destination, a.Count, s)
}

// handlePlace handles a Place stack request action.
func (h *ItemStackRequestHandler) handlePlace(a *protocol.PlaceStackRequestAction, s *Session) error {
	// Fix the container IDs before updating anything, otherwise we could have issues with future requests.
	// These issues only happen on place, hence why we're only checking for wrong container IDs here.
	a.Source.ContainerID = fixID(a.Source.ContainerID)
	a.Destination.ContainerID = fixID(a.Destination.ContainerID)

	return h.handleTransfer(a.Source, a.Destination, a.Count, s)
}

// handleSwap handles a Swap stack request action.
func (h *ItemStackRequestHandler) handleSwap(a *protocol.SwapStackRequestAction, s *Session) error {
	if err := h.verifySlots(s, a.Source, a.Destination); err != nil {
		return fmt.Errorf("slot out of sync: %w", err)
	}
	i, _ := h.itemInSlot(a.Source, s)
	dest, _ := h.itemInSlot(a.Destination, s)

	h.setItemInSlot(a.Source, dest, s)
	h.setItemInSlot(a.Destination, i, s)

	return nil
}

// handleCraft handles the Craft stack request action.
func (h *ItemStackRequestHandler) handleCraft(a *protocol.CraftRecipeStackRequestAction, s *Session) error {
	r, ok := s.recipeMapping[a.RecipeNetworkID]
	if !ok {
		return fmt.Errorf("invalid recipe network id sent")
	}

	var expectedInputs []recipes.Item
	var output item.Stack

	switch r := r.(type) {
	case recipes.ShapelessRecipe:
		expectedInputs, output = r.Inputs, r.Output
	case recipes.ShapedRecipe:
		expectedInputs, output = r.Inputs, r.Output
	default:
		return fmt.Errorf("tried crafting an invalid recipe: %T\n", r)
	}

	if !h.hasRequiredInputs(expectedInputs, s) {
		return fmt.Errorf("tried crafting without required inputs")
	}

	if err := h.removeInputs(expectedInputs, s); err != nil {
		return err
	}

	h.setItemInSlot(protocol.StackRequestSlotInfo{
		ContainerID:    containerCraftingResult,
		Slot:           craftingResultIndex,
		StackNetworkID: item_id(output),
	}, output, s)

	return nil
}

// handleCreativeCraft handles the CreativeCraft request action.
func (h *ItemStackRequestHandler) handleCreativeCraft(a *protocol.CraftCreativeStackRequestAction, s *Session) error {
	if !s.c.GameMode().CreativeInventory() {
		return fmt.Errorf("can only craft creative items in gamemode creative/spectator")
	}
	index := a.CreativeItemNetworkID - 1
	if int(index) >= len(creative.Items()) {
		return fmt.Errorf("creative item with network ID %v does not exist", index)
	}
	it := creative.Items()[index]
	it = it.Grow(it.MaxCount() - 1)

	h.setItemInSlot(protocol.StackRequestSlotInfo{
		ContainerID:    containerCreativeOutput,
		Slot:           50,
		StackNetworkID: item_id(it),
	}, it, s)
	return nil
}

// handleDestroy handles the destroying of an item by moving it into the creative inventory.
func (h *ItemStackRequestHandler) handleDestroy(a *protocol.DestroyStackRequestAction, s *Session) error {
	if h.ignoreDestroy {
		return nil
	}
	if !s.c.GameMode().CreativeInventory() {
		return fmt.Errorf("can only destroy items in gamemode creative/spectator")
	}
	if err := h.verifySlot(a.Source, s); err != nil {
		return fmt.Errorf("source slot out of sync: %w", err)
	}
	i, _ := h.itemInSlot(a.Source, s)
	if i.Count() < int(a.Count) {
		return fmt.Errorf("client attempted to destroy %v items, but only %v present", a.Count, i.Count())
	}

	h.setItemInSlot(a.Source, i.Grow(-int(a.Count)), s)
	return nil
}

// handleDrop handles the dropping of an item by moving it outside of the inventory while having the
// inventory opened.
func (h *ItemStackRequestHandler) handleDrop(a *protocol.DropStackRequestAction, s *Session) error {
	if err := h.verifySlot(a.Source, s); err != nil {
		return fmt.Errorf("source slot out of sync: %w", err)
	}
	i, _ := h.itemInSlot(a.Source, s)
	if i.Count() < int(a.Count) {
		return fmt.Errorf("client attempted to drop %v items, but only %v present", a.Count, i.Count())
	}

	if s.c.Drop(i.Grow(int(a.Count)-i.Count())) != 0 {
		h.setItemInSlot(a.Source, i.Grow(-int(a.Count)), s)
	} else {
		h.setItemInSlot(a.Source, i, s)
	}
	return nil
}

// handleBeaconPayment handles the selection of effects in a beacon and the removal of the item used to pay
// for those effects.
func (h *ItemStackRequestHandler) handleBeaconPayment(a *protocol.BeaconPaymentStackRequestAction, s *Session) error {
	slot := protocol.StackRequestSlotInfo{
		ContainerID: containerBeacon,
		Slot:        0x1b,
	}
	// First check if there actually is a beacon opened.
	if !s.containerOpened.Load() {
		return fmt.Errorf("no beacon container opened")
	}
	pos := s.openedPos.Load().(cube.Pos)
	beacon, ok := s.c.World().Block(pos).(block.Beacon)
	if !ok {
		return fmt.Errorf("no beacon container opened")
	}

	// Check if the item present in the beacon slot is valid.
	payment, _ := h.itemInSlot(slot, s)
	if payable, ok := payment.Item().(item.BeaconPayment); !ok || !payable.PayableForBeacon() {
		return fmt.Errorf("item %#v in beacon slot cannot be used as payment", payment)
	}

	// Check if the effects are valid and allowed for the beacon's level.
	if !h.validBeaconEffect(a.PrimaryEffect, beacon) {
		return fmt.Errorf("primary effect selected is not allowed: %v for level %v", a.PrimaryEffect, beacon.Level())
	} else if !h.validBeaconEffect(a.SecondaryEffect, beacon) || (beacon.Level() < 4 && a.SecondaryEffect != 0) {
		return fmt.Errorf("secondary effect selected is not allowed: %v for level %v", a.SecondaryEffect, beacon.Level())
	}

	beacon.Primary, _ = effect.ByID(int(a.PrimaryEffect))
	beacon.Secondary, _ = effect.ByID(int(a.SecondaryEffect))
	s.c.World().SetBlock(pos, beacon)

	// The client will send a Destroy action after this action, but we can't rely on that because the client
	// could just not send it.
	// We just ignore the next Destroy action and set the item to air here.
	h.setItemInSlot(slot, item.NewStack(block.Air{}, 0), s)
	h.ignoreDestroy = true
	return nil
}

// validBeaconEffect checks if the ID passed is a valid beacon effect.
func (h *ItemStackRequestHandler) validBeaconEffect(id int32, beacon block.Beacon) bool {
	switch id {
	case 1, 3:
		return beacon.Level() >= 1
	case 8, 11:
		return beacon.Level() >= 2
	case 5:
		return beacon.Level() >= 3
	case 10:
		return beacon.Level() >= 4
	case 0:
		return true
	}
	return false
}

// handleTransfer handles the transferring of x count from a source slot to a destination slot.
func (h *ItemStackRequestHandler) handleTransfer(from, to protocol.StackRequestSlotInfo, count byte, s *Session) error {
	if err := h.verifySlots(s, from, to); err != nil {
		return fmt.Errorf("source slot out of sync: %w", err)
	}
	i, _ := h.itemInSlot(from, s)
	dest, _ := h.itemInSlot(to, s)
	if !i.Comparable(dest) {
		return fmt.Errorf("client tried transferring %v to %v, but the stacks are incomparable", i, dest)
	}
	if i.Count() < int(count) {
		return fmt.Errorf("client tried subtracting %v from item count, but there are only %v", count, i.Count())
	}
	if (dest.Count()+int(count) > dest.MaxCount()) && !dest.Empty() {
		return fmt.Errorf("client tried adding %v to item count %v, but max is %v", count, dest.Count(), dest.MaxCount())
	}
	if dest.Empty() {
		dest = i.Grow(-math.MaxInt32)
	}

	h.setItemInSlot(from, i.Grow(-int(count)), s)
	h.setItemInSlot(to, dest.Grow(int(count)), s)

	return nil
}

// verifySlots verifies a list of slots passed.
func (h *ItemStackRequestHandler) verifySlots(s *Session, slots ...protocol.StackRequestSlotInfo) error {
	for _, slot := range slots {
		if err := h.verifySlot(slot, s); err != nil {
			return err
		}
	}
	return nil
}

// verifySlot checks if the slot passed by the client is the same as that expected by the server.
func (h *ItemStackRequestHandler) verifySlot(slot protocol.StackRequestSlotInfo, s *Session) error {
	h.tryAcknowledgeChanges(slot)
	if len(h.responseChanges) > 256 {
		return fmt.Errorf("too many unacknowledged request slot changes")
	}

	i, err := h.itemInSlot(slot, s)
	if err != nil {
		return err
	}
	clientID, err := h.resolveID(slot)
	if err != nil {
		return err
	}
	// The client seems to send negative stack network IDs for predictions, which we can ignore. We'll simply
	// override this network ID later.
	if id := item_id(i); id != clientID {
		return fmt.Errorf("stack ID mismatch: client expected %v, but server had %v", clientID, id)
	}
	inventory, _ := s.invByID(int32(slot.ContainerID))
	if inventory.SlotLocked(int(slot.Slot)) {
		return fmt.Errorf("slot in inventory was locked")
	}
	return nil
}

// resolveID resolves the stack network ID in the slot passed. If it is negative, it points to an earlier
// request, in which case it will look it up in the changes of an earlier response to a request to find the
// actual stack network ID in the slot. If it is positive, the ID will be returned again.
func (h *ItemStackRequestHandler) resolveID(slot protocol.StackRequestSlotInfo) (int32, error) {
	if slot.StackNetworkID >= 0 {
		return slot.StackNetworkID, nil
	}
	containerChanges, ok := h.responseChanges[slot.StackNetworkID]
	if !ok {
		return 0, fmt.Errorf("slot pointed to stack request %v, but request could not be found", slot.StackNetworkID)
	}
	changes, ok := containerChanges[slot.ContainerID]
	if !ok {
		return 0, fmt.Errorf("slot pointed to stack request %v with container %v, but that container was not changed in the request", slot.StackNetworkID, slot.ContainerID)
	}
	actual, ok := changes[slot.Slot]
	if !ok {
		return 0, fmt.Errorf("slot pointed to stack request %v with container %v and slot %v, but that slot was not changed in the request", slot.StackNetworkID, slot.ContainerID, slot.Slot)
	}
	return actual.id, nil
}

// tryAcknowledgeChanges iterates through all cached response changes and checks if the stack request slot
// info passed from the client has the right stack network ID in any of the stored slots. If this is the case,
// that entry is removed, so that the maps are cleaned up eventually.
func (h *ItemStackRequestHandler) tryAcknowledgeChanges(slot protocol.StackRequestSlotInfo) {
	for requestID, containerChanges := range h.responseChanges {
		for containerID, changes := range containerChanges {
			for slotIndex, val := range changes {
				if (slot.Slot == slotIndex && slot.StackNetworkID >= 0 && slot.ContainerID == containerID) || h.current.Sub(val.timestamp) > time.Second*5 {
					delete(changes, slotIndex)
				}
			}
			if len(changes) == 0 {
				delete(containerChanges, containerID)
			}
		}
		if len(containerChanges) == 0 {
			delete(h.responseChanges, requestID)
		}
	}
}

// itemInSlot looks for the item in the slot as indicated by the slot info passed.
func (h *ItemStackRequestHandler) itemInSlot(slot protocol.StackRequestSlotInfo, s *Session) (item.Stack, error) {
	inventory, ok := s.invByID(int32(slot.ContainerID))
	if !ok {
		return item.Stack{}, fmt.Errorf("unable to find container with ID %v", slot.ContainerID)
	}
	i, err := inventory.Item(int(slot.Slot))
	if err != nil {
		return i, err
	}
	return i, nil
}

// setItemInSlot sets an item stack in the slot of a container present in the slot info.
func (h *ItemStackRequestHandler) setItemInSlot(slot protocol.StackRequestSlotInfo, i item.Stack, s *Session) {
	inventory, _ := s.invByID(int32(slot.ContainerID))

	_ = inventory.SetItem(int(slot.Slot), i)

	if h.changes[slot.ContainerID] == nil {
		h.changes[slot.ContainerID] = map[byte]protocol.StackResponseSlotInfo{}
	}
	respSlot := protocol.StackResponseSlotInfo{
		Slot:           slot.Slot,
		HotbarSlot:     slot.Slot,
		Count:          byte(i.Count()),
		StackNetworkID: item_id(i),
	}
	h.changes[slot.ContainerID][slot.Slot] = respSlot

	if h.responseChanges[h.currentRequest] == nil {
		h.responseChanges[h.currentRequest] = map[byte]map[byte]responseChange{}
	}
	if h.responseChanges[h.currentRequest][slot.ContainerID] == nil {
		h.responseChanges[h.currentRequest][slot.ContainerID] = map[byte]responseChange{}
	}
	h.responseChanges[h.currentRequest][slot.ContainerID][slot.Slot] = responseChange{
		id:        respSlot.StackNetworkID,
		timestamp: h.current,
	}
}

// resolve resolves the request with the ID passed.
func (h *ItemStackRequestHandler) resolve(id int32, s *Session) {
	info := make([]protocol.StackResponseContainerInfo, 0, len(h.changes))
	for container, slotInfo := range h.changes {
		slots := make([]protocol.StackResponseSlotInfo, 0, len(slotInfo))
		for _, slot := range slotInfo {
			slots = append(slots, slot)
		}
		info = append(info, protocol.StackResponseContainerInfo{
			ContainerID: container,
			SlotInfo:    slots,
		})
	}
	s.writePacket(&packet.ItemStackResponse{Responses: []protocol.ItemStackResponse{{
		Status:        protocol.ItemStackResponseStatusOK,
		RequestID:     id,
		ContainerInfo: info,
	}}})
	h.changes = map[byte]map[byte]protocol.StackResponseSlotInfo{}
}

// reject rejects the item stack request sent by the client so that it is reverted client-side.
func (h *ItemStackRequestHandler) reject(id int32, s *Session) {
	s.writePacket(&packet.ItemStackResponse{
		Responses: []protocol.ItemStackResponse{{
			Status:    protocol.ItemStackResponseStatusError,
			RequestID: id,
		}},
	})
	h.changes = map[byte]map[byte]protocol.StackResponseSlotInfo{}
}

// hasRequiredInputs checks and validates the inputs for a crafting grid.
func (h *ItemStackRequestHandler) hasRequiredInputs(inputs []recipes.Item, s *Session) bool {
	offset := s.getCraftingOffset()

	var satisfiedInputs int
	for i := byte(0); i < s.getCraftingSize(); i++ {
		if satisfiedInputs == len(inputs) {
			break
		}

		slot := i + offset
		oldSt, err := s.ui.Item(int(slot))
		if err != nil {
			return false
		}
		if oldSt.Empty() {
			// We should still up the satisfied inputs count if both stacks are empty.
			if inputs[satisfiedInputs].Empty() {
				satisfiedInputs++
			}

			continue
		}

		currentInputToMatch := inputs[satisfiedInputs]

		// Items that apply to all types, so we just compare with the name and count.
		if currentInputToMatch.AppliesToAll {
			name, _ := oldSt.Item().EncodeItem()
			otherName, _ := currentInputToMatch.Item().EncodeItem()
			if name == otherName && oldSt.Count() >= currentInputToMatch.Count() {
				satisfiedInputs++
			}
		} else {
			if oldSt.Comparable(currentInputToMatch.Stack) {
				satisfiedInputs++
			}
		}
	}

	return satisfiedInputs == len(inputs)
}

// removeInputs removes the inputs passed in the crafting grid.
func (h *ItemStackRequestHandler) removeInputs(inputs []recipes.Item, s *Session) error {
	offset := s.getCraftingOffset()

	var index int
	for i := byte(0); i < s.getCraftingSize(); i++ {
		if index == len(inputs) {
			break
		}

		slot := i + offset
		oldSt, err := s.ui.Item(int(slot))
		if err != nil {
			return fmt.Errorf("expected item doesn't exist: " + err.Error())
		}
		if oldSt.Empty() {
			// We should still up the index if the expected input is empty.
			if inputs[index].Empty() {
				index++
			}

			continue
		}

		st := oldSt.Grow(-inputs[index].Count())
		h.setItemInSlot(protocol.StackRequestSlotInfo{
			ContainerID:    containerCraftingGrid,
			Slot:           slot,
			StackNetworkID: item_id(st),
		}, st, s)
		index++
	}

	return nil
}
