package settings

import (
	"testing"

	captypes "crude/types/capability"
)

func TestCapabilityActionsFitTheirOwnPaintAndHitRows(test *testing.T) {
	_, root, host := capabilityFixture(test, captypes.KindSkill)
	for _, width := range []int{72, 80, 100, 130} {
		host.Resize(width, 24)
		host.MarkLayout()
		host.Layout()
		for _, bar := range root.capCatalog.actionBars() {
			for _, action := range []string{"create-skill", "add-local", "inspect", "disable", "remove", "sources", "acquisition", "snapshot"} {
				if rect, exists := bar.ButtonBounds("settings.capability." + action); exists && rect.X+rect.W > bar.Bounds().W {
					test.Fatalf("%d columns: %s escapes its own action row: %+v in %+v", width, action, rect, bar.Bounds())
				}
			}
		}
	}
	if root.capCatalog.Bounds().W == 0 {
		test.Fatal("catalog was not laid out")
	}
}
