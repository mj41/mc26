package chat

// HoverEvent (style_gen.go) is the tooltip of a text component: its Action
// selects the case and the case's fields carry the content.

// ShowText shows a text component as the tooltip.
func ShowText(text Message) *HoverEvent {
	return &HoverEvent{Action: HoverEventActionShowText, Value: &text}
}

// ShowItem shows an item's tooltip (the item id, e.g. "minecraft:diamond").
func ShowItem(item string) *HoverEvent {
	return &HoverEvent{Action: HoverEventActionShowItem, ID: item, Count: 1}
}

// ShowEntity shows an entity's tooltip (the entity type id, e.g. "minecraft:player").
func ShowEntity(entityType string) *HoverEvent {
	return &HoverEvent{Action: HoverEventActionShowEntity, ID: entityType}
}
