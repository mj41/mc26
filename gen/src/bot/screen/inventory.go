package screen

import (
	"errors"

	"github.com/mj41/go-mc26/data/constants"
)

// Inventory is the player's own inventory menu (InventoryMenu): the crafting
// result and grid, the armor, the main inventory, the hotbar and the offhand,
// at the slot numbers the constants of data/constants give.
type Inventory struct {
	Slots [constants.InventoryMenuShieldSlot + 1]Slot
}

func (inv *Inventory) onClose() error {
	return nil
}

func (inv *Inventory) onSetSlot(i int, s Slot) error {
	if i < 0 || i >= len(inv.Slots) {
		return errors.New("slot index out of bounds")
	}
	inv.Slots[i] = s
	return nil
}

func (inv *Inventory) CraftingOutput() *Slot { return &inv.Slots[constants.InventoryMenuResultSlot] }
func (inv *Inventory) CraftingInput() []Slot {
	return inv.Slots[constants.InventoryMenuCraftSlotStart:constants.InventoryMenuCraftSlotEnd]
}

// Armor returns the armor section of the Inventory: head, chest, legs and feet.
func (inv *Inventory) Armor() []Slot {
	return inv.Slots[constants.InventoryMenuArmorSlotStart:constants.InventoryMenuArmorSlotEnd]
}
func (inv *Inventory) Main() []Slot {
	return inv.Slots[constants.InventoryMenuInvSlotStart:constants.InventoryMenuInvSlotEnd]
}
func (inv *Inventory) Hotbar() []Slot {
	return inv.Slots[constants.InventoryMenuUseRowSlotStart:constants.InventoryMenuUseRowSlotEnd]
}
func (inv *Inventory) Offhand() *Slot { return &inv.Slots[constants.InventoryMenuShieldSlot] }
