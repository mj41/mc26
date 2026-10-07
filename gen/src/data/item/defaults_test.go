package item

import (
	"testing"

	"github.com/mj41/go-mc26/level/component"
)

func TestDefaultComponents(t *testing.T) {
	for id := range defaultWire {
		if _, err := DefaultComponents(ID(id)); err != nil {
			t.Fatalf("item %d: %v", id, err)
		}
	}
	tool := DefaultComponent[*component.Tool](WoodenPickaxe.ID)
	if tool == nil || len(tool.Rules) == 0 {
		t.Fatalf("a wooden pickaxe has no tool rules: %+v", tool)
	}
	if food := DefaultComponent[*component.Food](Bread.ID); food == nil || food.Nutrition != 5 {
		t.Errorf("bread: %+v", food)
	}
	if DefaultComponent[*component.Tool](Stone.ID) != nil {
		t.Errorf("stone is a tool")
	}
}
