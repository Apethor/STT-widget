package desktop

import (
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type tabSelectButton struct {
	widget.Button
	secondary func(*fyne.PointEvent)
	dragged   func(*fyne.DragEvent)
	dragEnd   func()
}

func (b *tabSelectButton) Dragged(event *fyne.DragEvent) {
	if b.dragged != nil {
		b.dragged(event)
	}
}

func (b *tabSelectButton) DragEnd() {
	if b.dragEnd != nil {
		b.dragEnd()
	}
}

func (b *tabSelectButton) TappedSecondary(event *fyne.PointEvent) {
	if !b.Disabled() && b.secondary != nil {
		b.secondary(event)
	}
}

type tabHeader struct {
	item  *container.TabItem
	group uint64
	title *tabSelectButton
	close *widget.Button
	box   *fyne.Container
}

type tabGroupRow struct {
	id      uint64
	label   *groupLabel
	buttons *fyne.Container
	scroll  *container.Scroll
	box     *fyne.Container
	actions *fyne.Container
	create  *widget.Button
	more    *widget.Button
	resize  *groupWidthHandle
	marker  *canvas.Rectangle
}

type tabDropTarget struct {
	group uint64
	index int
	valid bool
}

type documentTabsRenderer struct {
	tabs           *documentTabs
	headers        []tabHeader
	selected       *container.TabItem
	scrollSelected bool
	buttons        *fyne.Container
	scroll         *container.Scroll
	header         *fyne.Container
	body           *fyne.Container
	root           *fyne.Container
	divider        *canvas.Rectangle
	create         *widget.Button
	more           *widget.Button
	objects        []fyne.CanvasObject
	groupIDs       []uint64
	rows           []*tabGroupRow
	rowsContent    *fyne.Container
	groupScroll    *container.Scroll
	createGroup    *widget.Button
	tabMarker      *tabDropTarget
	groupMarker    *canvas.Rectangle
	groupDropIndex int
	selectedWidth  float32
}

func newDocumentTabsRenderer(tabs *documentTabs) *documentTabsRenderer {
	r := &documentTabsRenderer{
		tabs: tabs, body: container.NewStack(), rowsContent: container.NewVBox(),
	}
	r.createGroup = widget.NewButtonWithIcon("", theme.ContentAddIcon(), func() {
		if r.tabs.createGroup != nil {
			r.tabs.createGroup()
		}
	})
	r.createGroup.Importance = widget.LowImportance
	r.groupMarker = canvas.NewRectangle(theme.Color(theme.ColorNamePrimary))
	r.groupMarker.SetMinSize(fyne.NewSize(1, 2))
	r.groupDropIndex = -1
	r.groupScroll = container.NewVScroll(r.rowsContent)
	r.divider = canvas.NewRectangle(theme.Color(borderColor))
	r.divider.SetMinSize(fyne.NewSize(1, 1))
	r.header = container.NewVBox(
		container.NewBorder(nil, nil, r.createGroup, nil, r.groupScroll),
		r.divider,
	)
	r.root = container.NewBorder(r.header, nil, nil, nil, r.body)
	r.objects = []fyne.CanvasObject{r.root}
	r.update()
	return r
}

func (r *documentTabsRenderer) update() {
	groups := []uint64{1}
	if r.tabs.groupIDs != nil {
		groups = r.tabs.groupIDs()
	}
	changed := len(r.headers) != len(r.tabs.Items) || !slices.Equal(groups, r.groupIDs)
	if !changed {
		index := 0
		for _, group := range groups {
			for _, item := range r.tabs.Items {
				if r.groupFor(item) != group {
					continue
				}
				if r.headers[index].item != item || r.headers[index].group != group {
					changed = true
				}
				index++
			}
		}
	}
	if changed {
		r.groupIDs = append([]uint64{}, groups...)
		r.tabMarker = nil
		r.groupDropIndex = -1
		r.headers = make([]tabHeader, 0, len(r.tabs.Items))
		r.rows, r.rowsContent.Objects = nil, nil
		for _, group := range groups {
			row := r.createGroupRow(group)
			r.rows = append(r.rows, row)
			r.rowsContent.Objects = append(r.rowsContent.Objects, row.box)
			for _, item := range r.tabs.Items {
				if r.groupFor(item) != group {
					continue
				}
				header := r.createHeader(item, group)
				r.headers = append(r.headers, header)
				row.buttons.Add(header.box)
			}
		}
		r.rowsContent.Refresh()
		r.scrollSelected = true
	}
	selected := r.tabs.Selected()
	var rowHeight float32
	for index, row := range r.rows {
		active := selected != nil && row.id == r.groupFor(selected)
		if selected == nil {
			active = index == 0
		}
		row.label.TextStyle.Bold = active
		actions := []fyne.CanvasObject{row.create, row.more}
		if active && r.tabs.undoClose != nil {
			actions = append([]fyne.CanvasObject{r.tabs.undoClose}, actions...)
		}
		if !slices.Equal(row.actions.Objects, actions) {
			row.actions.Objects = actions
			row.actions.Refresh()
		}
		if r.tabs.groupName != nil {
			row.label.SetText(r.tabs.groupName(row.id))
		}
		row.label.Refresh()
		rowHeight = max(rowHeight, row.box.MinSize().Height)
		if selected != nil && row.id == r.groupFor(selected) {
			r.buttons, r.scroll, r.create, r.more = row.buttons, row.scroll, row.create, row.more
		}
	}
	visibleRows := min(4, len(r.rows))
	r.groupScroll.SetMinSize(fyne.NewSize(0, float32(visibleRows)*rowHeight+float32(max(0, visibleRows-1))*theme.Size(theme.SizeNamePadding)))
	if len(r.tabs.Items) >= maxDocuments {
		r.createGroup.Disable()
	} else {
		r.createGroup.Enable()
	}
	for _, header := range r.headers {
		header.title.Importance = widget.LowImportance
		if header.item == selected {
			header.title.Importance = widget.MediumImportance
		}
		header.title.SetText(header.item.Text)
		header.title.Refresh()
	}
	if selected != r.selected {
		r.selected = selected
		r.body.Objects = nil
		if selected != nil {
			r.body.Objects = []fyne.CanvasObject{selected.Content}
		}
		r.scrollSelected = true
	}
	r.divider.FillColor = r.tabs.Theme().Color(borderColor, theme.VariantDark)
	r.divider.Refresh()
}

func (r *documentTabsRenderer) groupFor(item *container.TabItem) uint64 {
	if r.tabs.groupFor != nil {
		return r.tabs.groupFor(item)
	}
	return 1
}

func (r *documentTabsRenderer) createGroupRow(group uint64) *tabGroupRow {
	label := &groupLabel{
		Label: widget.Label{Truncation: fyne.TextTruncateEllipsis},
		limit: func() float32 {
			if r.tabs.groupNameLimit != nil {
				return r.tabs.groupNameLimit(group)
			}
			return defaultGroupNameWidth
		},
		secondary: func(event *fyne.PointEvent) {
			if r.tabs.onGroupSecondary != nil {
				r.tabs.onGroupSecondary(group, event)
			}
		},
	}
	label.dragged = func(event *fyne.DragEvent) { r.updateGroupDrag(group, label, event) }
	label.dragEnd = func() { r.finishGroupDrag(group) }
	label.ExtendBaseWidget(label)
	row := &tabGroupRow{id: group, buttons: container.New(layout.NewCustomPaddedHBoxLayout(2)), label: label}
	row.marker = canvas.NewRectangle(r.tabs.Theme().Color(theme.ColorNamePrimary, theme.VariantDark))
	row.marker.SetMinSize(fyne.NewSize(2, 1))
	row.scroll = container.NewHScroll(row.buttons)
	row.resize = newGroupWidthHandle(nil, nil, nil)
	if r.tabs.groupNameHandle != nil {
		row.resize = r.tabs.groupNameHandle(group)
	}
	row.create = widget.NewButtonWithIcon("", theme.ContentAddIcon(), func() {
		var item *container.TabItem
		if selected := r.tabs.Selected(); selected != nil && r.groupFor(selected) == group && r.tabs.CreateTab != nil {
			item = r.tabs.CreateTab()
		} else if r.tabs.createInGroup != nil {
			item = r.tabs.createInGroup(group)
		}
		if item != nil {
			r.tabs.Append(item)
			r.tabs.Select(item)
		}
	})
	row.create.Importance = widget.LowImportance
	row.more = widget.NewButtonWithIcon("", theme.MoreHorizontalIcon(), func() { r.showGroupTabs(row) })
	row.more.Importance = widget.LowImportance
	row.actions = container.NewHBox(row.create, row.more)
	row.box = container.New(groupRowLayout{}, row.label, row.resize,
		container.NewThemeOverride(row.scroll, tabScrollTheme{Theme: r.tabs.Theme()}), row.actions)
	return row
}

func (r *documentTabsRenderer) createHeader(item *container.TabItem, group uint64) tabHeader {
	button := &tabSelectButton{
		Button: widget.Button{Text: item.Text, OnTapped: func() { r.tabs.Select(item) }},
		secondary: func(event *fyne.PointEvent) {
			if r.tabs.onSecondary != nil {
				r.tabs.onSecondary(item, event)
			}
		},
	}
	button.dragged = func(event *fyne.DragEvent) { r.updateTabDrag(item, button, event) }
	button.dragEnd = func() { r.finishTabDrag(item) }
	button.ExtendBaseWidget(button)
	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		if r.tabs.CloseIntercept != nil {
			r.tabs.CloseIntercept(item)
		} else {
			r.tabs.Remove(item)
			if r.tabs.OnClosed != nil {
				r.tabs.OnClosed(item)
			}
		}
	})
	closeButton.Importance = widget.LowImportance
	return tabHeader{item: item, group: group, title: button, close: closeButton,
		box: container.New(layout.NewCustomPaddedHBoxLayout(0), button, closeButton)}
}

func (r *documentTabsRenderer) showGroupTabs(row *tabGroupRow) {
	canvas := fyne.CurrentApp().Driver().CanvasForObject(row.more)
	if canvas == nil {
		return
	}
	items := make([]*fyne.MenuItem, 0, len(r.tabs.Items))
	for _, tab := range r.tabs.Items {
		if r.groupFor(tab) != row.id {
			continue
		}
		item := fyne.NewMenuItem(tab.Text, func() { r.tabs.Select(tab) })
		item.Checked = tab == r.tabs.Selected()
		items = append(items, item)
	}
	menuDefinition := fyne.NewMenu(uiText("Abas"), items...)
	if r.tabs.groupMenu != nil {
		menuDefinition = r.tabs.groupMenu(row.id)
	}
	menu := widget.NewPopUpMenu(menuDefinition, canvas)
	menu.ShowAtRelativePosition(fyne.NewPos(0, row.more.Size().Height), row.more)
}

func (r *documentTabsRenderer) Layout(size fyne.Size) {
	r.root.Resize(size)
	if r.scroll != nil && r.scroll.Size().Width != r.selectedWidth {
		r.selectedWidth = r.scroll.Size().Width
		r.scrollSelected = true
	}
	if r.scrollSelected && r.scroll != nil && r.scroll.Size().Width > 0 {
		for _, header := range r.headers {
			if header.item != r.selected {
				continue
			}
			left, right := header.box.Position().X, header.box.Position().X+header.box.Size().Width
			offset := r.scroll.Offset.X
			if left < offset {
				offset = left
			} else if right > offset+r.scroll.Size().Width {
				offset = max(0, right-r.scroll.Size().Width)
			}
			r.scroll.Offset.X = offset
			r.scroll.Refresh()
			break
		}
		for _, row := range r.rows {
			if r.selected == nil || row.id != r.groupFor(r.selected) {
				continue
			}
			top, bottom := row.box.Position().Y, row.box.Position().Y+row.box.Size().Height
			offset := r.groupScroll.Offset.Y
			if top < offset {
				offset = top
			} else if bottom > offset+r.groupScroll.Size().Height {
				offset = max(0, bottom-r.groupScroll.Size().Height)
			}
			r.groupScroll.Offset.Y = offset
			r.groupScroll.Refresh()
			break
		}
		r.scrollSelected = false
	}
}

func (r *documentTabsRenderer) MinSize() fyne.Size { return r.root.MinSize() }

func (r *documentTabsRenderer) Refresh() {
	r.update()
	r.Layout(r.tabs.Size())
	r.root.Refresh()
	canvas.Refresh(r.tabs)
}

func (r *documentTabsRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *documentTabsRenderer) Destroy()                     {}

func (t *documentTabs) tabBarHeight() float32 {
	if t.renderer != nil {
		return t.renderer.header.MinSize().Height + theme.Size(theme.SizeNamePadding)
	}
	return 40
}
