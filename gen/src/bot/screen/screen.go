package screen

import (
	"errors"

	"github.com/mj41/go-mc26/bot"
	"github.com/mj41/go-mc26/data/packetid"
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/play"
	"github.com/mj41/go-mc26/protocol/types"
)

type Manager struct {
	c *bot.Client

	Screens   map[int]Container
	Inventory Inventory
	Cursor    Slot
	events    EventsListener
	// The last received State ID from server
	stateID int32
}

func NewManager(c *bot.Client, e EventsListener) *Manager {
	m := &Manager{
		c:       c,
		Screens: make(map[int]Container),
		events:  e,
	}
	m.Screens[0] = &m.Inventory
	c.Events.AddListener(
		bot.PacketHandler{Priority: 0, ID: packetid.ClientboundPlayOpenScreen, F: m.onOpenScreen},
		bot.PacketHandler{Priority: 0, ID: packetid.ClientboundPlayContainerSetContent, F: m.onSetContentPacket},
		bot.PacketHandler{Priority: 0, ID: packetid.ClientboundPlayContainerClose, F: m.onCloseScreen},
		bot.PacketHandler{Priority: 0, ID: packetid.ClientboundPlayContainerSetSlot, F: m.onSetSlot},
		bot.PacketHandler{Priority: 0, ID: packetid.ClientboundPlaySetPlayerInventory, F: m.onSetPlayerInventory},
	)
	return m
}

// ChangedSlots lists the slots a click changed on the client's side, by slot
// number; a nil Slot means the slot became empty.
type ChangedSlots map[int16]*Slot

// ContainerClick sends a click on slot of container id. changed and carried
// (the cursor after the click) are sent as hashed stacks. Component hashes are
// not computed here: a stack with components goes out with empty component
// maps, and the server answers the mismatch by re-sending the slot.
func (m *Manager) ContainerClick(id int, slot int16, button byte, mode types.ContainerInput, changed ChangedSlots, carried *Slot) error {
	click := play.ContainerClick{
		ContainerID:    pk.VarInt(id),
		StateID:        pk.VarInt(m.stateID),
		SlotNum:        pk.Short(slot),
		ButtonNum:      pk.Byte(button),
		ContainerInput: mode,
		ChangedSlots:   make(types.Map[pk.Short, *pk.Short, hashedStack, *hashedStack], 0, len(changed)),
		CarriedItem:    hashedStackOf(carried),
	}
	for i, s := range changed {
		click.ChangedSlots = append(click.ChangedSlots, types.Entry[pk.Short, hashedStack]{Key: pk.Short(i), Val: hashedStackOf(s)})
	}
	return m.c.Conn.WritePacket(pk.Marshal(click.PacketID(), click))
}

// hashedStack is the wire form of a slot in a click: absent for an empty slot.
type hashedStack = pk.Option[types.HashedStackActualItem, *types.HashedStackActualItem]

func hashedStackOf(s *Slot) hashedStack {
	if s == nil || s.Count <= 0 {
		return hashedStack{}
	}
	return hashedStack{Has: true, Val: types.HashedStackActualItem{Item: s.ItemID, Count: s.Count}}
}

func (m *Manager) onOpenScreen(p pk.Packet) error {
	var os play.OpenScreen
	if err := p.Scan(&os); err != nil {
		return Error{err}
	}
	ContainerID, Type, Title := os.ContainerID, os.Type, os.Title
	if _, ok := m.Screens[int(ContainerID)]; !ok {
		TypeInt32 := int32(Type)
		if TypeInt32 < 6 {
			Rows := TypeInt32 + 1
			chest := Chest{
				Type:  TypeInt32,
				Slots: make([]Slot, 9*Rows),
				Rows:  int(Rows),
				Title: Title,
			}
			m.Screens[int(ContainerID)] = &chest
		}
	} else {
		return errors.New("container id already exists in screens")
	}
	if m.events.Open != nil {
		if err := m.events.Open(int(ContainerID), int32(Type), Title); err != nil {
			return Error{err}
		}
	}
	return nil
}

func (m *Manager) onSetContentPacket(p pk.Packet) error {
	var sc play.ContainerSetContent
	if err := p.Scan(&sc); err != nil {
		return Error{err}
	}
	ContainerID, StateID := sc.ContainerID, sc.StateID
	SlotData := []Slot(sc.Items)
	m.Cursor = sc.CarriedItem
	m.stateID = int32(StateID)
	// copy the slot data to container
	container, ok := m.Screens[int(ContainerID)]
	if !ok {
		// Unknown container ID: the server may send spurious updates for containers
		// the bot hasn't opened (e.g., after death/respawn or dimension change).
		// Silently ignore rather than crashing HandleGame.
		return nil
	}
	for i, v := range SlotData {
		err := container.onSetSlot(i, v)
		if err != nil {
			return Error{err}
		}
		if m.events.SetSlot != nil {
			if err := m.events.SetSlot(int(ContainerID), i); err != nil {
				return Error{err}
			}
		}
	}
	return nil
}

func (m *Manager) onCloseScreen(p pk.Packet) error {
	var cc play.ClientboundContainerClose
	if err := p.Scan(&cc); err != nil {
		return Error{err}
	}
	ContainerID := cc.ContainerID
	if c, ok := m.Screens[int(ContainerID)]; ok {
		delete(m.Screens, int(ContainerID))
		if err := c.onClose(); err != nil {
			return Error{err}
		}
		if m.events.Close != nil {
			if err := m.events.Close(int(ContainerID)); err != nil {
				return Error{err}
			}
		}
	}
	return nil
}

func (m *Manager) onSetSlot(p pk.Packet) (err error) {
	var ss play.ContainerSetSlot
	if err := p.Scan(&ss); err != nil {
		return Error{err}
	}
	ContainerID, StateID, SlotID := ss.ContainerID, ss.StateID, ss.Slot
	SlotData := ss.ItemStack

	m.stateID = int32(StateID)
	if ContainerID == -1 && SlotID == -1 {
		m.Cursor = SlotData
	} else if ContainerID == -2 {
		err = m.Inventory.onSetSlot(int(SlotID), SlotData)
	} else if c, ok := m.Screens[int(ContainerID)]; ok {
		err = c.onSetSlot(int(SlotID), SlotData)
	}

	if m.events.SetSlot != nil {
		if err := m.events.SetSlot(int(ContainerID), int(SlotID)); err != nil {
			return Error{err}
		}
	}
	if err != nil {
		return Error{err}
	}
	return nil
}

// onSetPlayerInventory handles ClientboundSetPlayerInventory (1.20.5+).
// Wire: VarInt(slotIndex) + Slot(data). Updates the player inventory directly.
func (m *Manager) onSetPlayerInventory(p pk.Packet) error {
	var spi play.SetPlayerInventory
	if err := p.Scan(&spi); err != nil {
		return Error{err}
	}
	SlotID, SlotData := spi.Slot, spi.Contents
	if err := m.Inventory.onSetSlot(int(SlotID), SlotData); err != nil {
		return Error{err}
	}
	if m.events.SetSlot != nil {
		if err := m.events.SetSlot(0, int(SlotID)); err != nil {
			return Error{err}
		}
	}
	return nil
}

// Slot is one slot of a container: the item stack the server sent, with its
// count, item id and component patch (types.ItemStack); count 0 is empty.
type Slot = types.ItemStack

type Container interface {
	onSetSlot(i int, s Slot) error
	onClose() error
}

type Error struct {
	Err error
}

func (e Error) Error() string {
	return "bot/screen: " + e.Err.Error()
}

func (e Error) Unwrap() error {
	return e.Err
}
