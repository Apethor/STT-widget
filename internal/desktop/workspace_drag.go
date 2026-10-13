package desktop

import (
	"fmt"
	"maps"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
)

func dragPosition(event *fyne.DragEvent, object fyne.CanvasObject) fyne.Position {
	if event.AbsolutePosition != (fyne.Position{}) {
		return event.AbsolutePosition
	}
	driver := fyne.CurrentApp().Driver()
	return driver.AbsolutePositionForObject(object).
		Add(event.Position).
		Add(fyne.NewPos(event.Dragged.DX, event.Dragged.DY))
}

func (r *documentTabsRenderer) updateTabDrag(item *container.TabItem, object fyne.CanvasObject, event *fyne.DragEvent) {
	if item == nil || r.tabs.moveTab == nil {
		return
	}
	position := dragPosition(event, object)
	if !r.dragInsideRows(position) {
		r.setTabDropTarget(nil)
		return
	}
	driver := fyne.CurrentApp().Driver()
	for _, row := range r.rows {
		top := driver.AbsolutePositionForObject(row.box).Y
		if position.Y < top || position.Y > top+row.box.Size().Height {
			continue
		}
		index := 0
		for _, header := range r.headers {
			if header.group != row.id {
				continue
			}
			left := driver.AbsolutePositionForObject(header.box).X
			if position.X >= left+header.box.Size().Width/2 {
				index++
				continue
			}
			break
		}
		r.setTabDropTarget(&tabDropTarget{group: row.id, index: index, valid: true})
		return
	}
	r.setTabDropTarget(nil)
}

func (r *documentTabsRenderer) updateGroupDrag(group uint64, object fyne.CanvasObject, event *fyne.DragEvent) {
	if r.tabs.moveGroup == nil || !slices.Contains(r.groupIDs, group) || len(r.rows) < 2 {
		return
	}
	position := dragPosition(event, object)
	if !r.dragInsideRows(position) {
		r.setGroupDropIndex(-1)
		return
	}
	driver := fyne.CurrentApp().Driver()
	first := driver.AbsolutePositionForObject(r.rows[0].box).Y
	lastRow := r.rows[len(r.rows)-1]
	last := driver.AbsolutePositionForObject(lastRow.box).Y + lastRow.box.Size().Height
	if position.Y < first || position.Y > last {
		r.setGroupDropIndex(-1)
		return
	}
	index := 0
	for _, row := range r.rows {
		top := driver.AbsolutePositionForObject(row.box).Y
		if position.Y >= top+row.box.Size().Height/2 {
			index++
		} else {
			break
		}
	}
	r.setGroupDropIndex(index)
}

func (r *documentTabsRenderer) dragInsideRows(position fyne.Position) bool {
	driver := fyne.CurrentApp().Driver()
	origin := driver.AbsolutePositionForObject(r.groupScroll)
	size := r.groupScroll.Size()
	canvas := driver.CanvasForObject(r.groupScroll)
	if canvas == nil || position.X < 0 || position.Y < 0 ||
		position.X >= canvas.Size().Width || position.Y >= canvas.Size().Height {
		return false
	}
	return position.X >= origin.X && position.X < origin.X+size.Width &&
		position.Y >= origin.Y && position.Y < origin.Y+size.Height
}

func (r *documentTabsRenderer) finishTabDrag(item *container.TabItem) {
	target := r.tabMarker
	r.setTabDropTarget(nil)
	if target == nil || r.tabs.moveTab == nil {
		return
	}
	r.tabs.moveTab(item, target.group, target.index)
}

func (r *documentTabsRenderer) finishGroupDrag(group uint64) {
	index := r.groupDropIndex
	r.setGroupDropIndex(-1)
	if index < 0 || r.tabs.moveGroup == nil {
		return
	}
	r.tabs.moveGroup(group, index)
}

func (r *documentTabsRenderer) setTabDropTarget(target *tabDropTarget) {
	if target != nil && !target.valid {
		target = nil
	}
	r.tabMarker = target
	for _, row := range r.rows {
		objects := make([]fyne.CanvasObject, 0, len(r.headers)+1)
		index := 0
		for _, header := range r.headers {
			if header.group != row.id {
				continue
			}
			if target != nil && target.group == row.id && target.index == index {
				objects = append(objects, row.marker)
			}
			objects = append(objects, header.box)
			index++
		}
		if target != nil && target.group == row.id && target.index >= index {
			objects = append(objects, row.marker)
		}
		row.buttons.Objects = objects
		row.buttons.Refresh()
	}
}

func (r *documentTabsRenderer) setGroupDropIndex(index int) {
	if index < 0 || index > len(r.rows) {
		index = -1
	}
	if r.groupDropIndex == index {
		return
	}
	r.groupDropIndex = index
	objects := make([]fyne.CanvasObject, 0, len(r.rows)+1)
	for current := 0; current <= len(r.rows); current++ {
		if current == index {
			objects = append(objects, r.groupMarker)
		}
		if current < len(r.rows) {
			objects = append(objects, r.rows[current].box)
		}
	}
	r.rowsContent.Objects = objects
	r.rowsContent.Refresh()
}

func (d *desktop) moveDocumentTab(item *container.TabItem, group uint64, index int) {
	if d.closing || d.tabsDialog || d.discardDialog {
		return
	}
	var moving *document
	for _, doc := range d.documents {
		if doc.item == item && !doc.closed {
			moving = doc
			break
		}
	}
	if moving == nil || !slices.Contains(d.groups, group) {
		return
	}
	sourceGroup := moving.group
	sourceIndex := slices.Index(d.groupDocuments(sourceGroup), moving)
	targetCount := len(d.groupDocuments(group))
	if sourceIndex < 0 {
		return
	}
	index = max(0, min(index, targetCount))
	if sourceGroup == group {
		if index > sourceIndex {
			index--
		}
		if index == sourceIndex {
			return
		}
	}

	oldDocuments := slices.Clone(d.documents)
	oldGroups := slices.Clone(d.groups)
	oldNames := maps.Clone(d.groupNames)
	oldAutomaticNames := maps.Clone(d.groupAutomaticNames)
	oldWidths := maps.Clone(d.groupNameWidths)
	oldGroup := moving.group
	selected := d.document
	d.cancelMergeForGroup(sourceGroup, "A ordem do grupo mudou. Clique em Mesclar para usar as abas atuais.")
	if sourceGroup != group {
		d.cancelMergeForGroup(group, "Uma aba mudou de grupo. Clique em Mesclar para usar as abas atuais.")
	}
	documentIndex := slices.Index(d.documents, moving)
	if documentIndex < 0 {
		return
	}
	d.documents = slices.Delete(d.documents, documentIndex, documentIndex+1)
	moving.group = group
	if sourceGroup != group && len(d.groupDocuments(sourceGroup)) == 0 && len(d.groups) > 1 {
		d.groups = slices.DeleteFunc(d.groups, func(candidate uint64) bool { return candidate == sourceGroup })
		delete(d.groupNames, sourceGroup)
		delete(d.groupAutomaticNames, sourceGroup)
		delete(d.groupNameWidths, sourceGroup)
	}
	insert := d.groupInsertion(group, index)
	d.documents = slices.Insert(d.documents, insert, moving)
	d.resetDocumentOrder(d.documents, d.groups, selected)
	if err := d.saveWorkspace(); err != nil {
		moving.group = oldGroup
		d.groupNames, d.groupNameWidths = oldNames, oldWidths
		d.groupAutomaticNames = oldAutomaticNames
		d.resetDocumentOrder(oldDocuments, oldGroups, selected)
		d.setTabMoveError(err)
	}
}

func (d *desktop) moveGroup(group uint64, index int) {
	if d.closing || d.tabsDialog || d.discardDialog {
		return
	}
	updated, changed := moveGroupInOrder(d.groups, group, index)
	if !changed {
		return
	}
	oldDocuments := slices.Clone(d.documents)
	oldGroups := slices.Clone(d.groups)
	selected := d.document
	ordered := make([]*document, 0, len(d.documents))
	for _, id := range updated {
		ordered = append(ordered, d.groupDocuments(id)...)
	}
	d.resetDocumentOrder(ordered, updated, selected)
	if err := d.saveWorkspace(); err != nil {
		d.resetDocumentOrder(oldDocuments, oldGroups, selected)
		d.setTabMoveError(err)
	}
}

func (d *desktop) resetDocumentOrder(documents []*document, groups []uint64, selected *document) {
	d.documents, d.groups = documents, groups
	d.selectingTabs = true
	d.tabs.SetItems(documentItems(d.documents))
	if selected != nil && !selected.closed {
		d.tabs.Select(selected.item)
	}
	d.selectingTabs = false
	d.renumberDocuments()
	d.tabs.Refresh()
	d.content.Refresh()
}

func moveGroupInOrder(groups []uint64, group uint64, index int) ([]uint64, bool) {
	sourceIndex := slices.Index(groups, group)
	if sourceIndex < 0 || index < 0 || index > len(groups) {
		return nil, false
	}
	updated := slices.Delete(slices.Clone(groups), sourceIndex, sourceIndex+1)
	if index > sourceIndex {
		index--
	}
	index = max(0, min(index, len(updated)))
	updated = slices.Insert(updated, index, group)
	return updated, !slices.Equal(groups, updated)
}

func (d *desktop) setTabMoveError(err error) {
	d.showError(fmt.Sprintf("Não foi possível salvar a nova posição da aba: %v", err), "")
}
