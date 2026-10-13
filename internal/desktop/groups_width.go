package desktop

import (
	"fmt"
	"image/color"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynedesktop "fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	defaultGroupNameWidth = float32(112)
	minGroupNameWidth     = float32(40)
	maxGroupNameWidth     = float32(320)
)

func (d *desktop) groupNameWidth(group uint64) float32 {
	if width := d.groupNameWidths[group]; width >= minGroupNameWidth && width <= maxGroupNameWidth {
		return width
	}
	return defaultGroupNameWidth
}

func (d *desktop) setGroupNameWidth(group uint64, width float32) {
	if width == defaultGroupNameWidth {
		delete(d.groupNameWidths, group)
	} else {
		d.groupNameWidths[group] = max(minGroupNameWidth, min(maxGroupNameWidth, width))
	}
	d.tabs.Refresh()
}

func (d *desktop) saveGroupNameWidth(group uint64, previous float32) {
	if !slices.Contains(d.groups, group) {
		delete(d.groupNameWidths, group)
		return
	}
	if err := d.saveWorkspace(); err != nil {
		d.setGroupNameWidth(group, previous)
		d.showError("Não foi possível salvar a largura do nome: "+err.Error(), "")
	}
}

func (d *desktop) newGroupNameHandle(group uint64) *groupWidthHandle {
	previous := d.groupNameWidth(group)
	adjusting := false
	handle := newGroupWidthHandle(
		func(delta float32) {
			if !slices.Contains(d.groups, group) {
				return
			}
			if !adjusting {
				previous, adjusting = d.groupNameWidth(group), true
			}
			d.setGroupNameWidth(group, d.groupNameWidth(group)+delta)
		},
		func() {
			if adjusting {
				d.saveGroupNameWidth(group, previous)
				adjusting = false
			}
		},
		func() {
			previous := d.groupNameWidth(group)
			d.setGroupNameWidth(group, defaultGroupNameWidth)
			d.saveGroupNameWidth(group, previous)
		},
	)
	return handle
}

type groupWidthForm struct {
	group   uint64
	popup   *widget.PopUp
	slider  *resetSlider
	value   *widget.Label
	syncing bool
}

func (d *desktop) openGroupNameWidth(group uint64) {
	if !slices.Contains(d.groups, group) {
		return
	}
	if d.groupWidthForm != nil {
		d.groupWidthForm.popup.Hide()
	}
	form := &groupWidthForm{
		group: group, slider: newResetSlider(float64(minGroupNameWidth), float64(maxGroupNameWidth), float64(defaultGroupNameWidth)),
		value: widget.NewLabel(""),
	}
	form.slider.SetValue(float64(d.groupNameWidth(group)))
	previous := d.groupNameWidth(group)
	title := settingLabel("Limite do nome · " + d.groupName(group))
	title.Truncation = fyne.TextTruncateEllipsis
	content := container.NewVBox(
		title,
		container.NewThemeOverride(container.NewBorder(nil, nil, nil, form.value, form.slider), volumeTheme{Theme: d.palette}),
		d.smallText(widget.NewLabel("Duplo clique na bolinha restaura 112.")),
	)
	form.popup = widget.NewPopUp(content, d.window.Canvas())
	d.groupWidthForm = form
	updateValue := func(width float64) { form.value.SetText(fmt.Sprintf("%.0f", width)) }
	updateValue(form.slider.Value)
	form.slider.OnChanged = func(width float64) {
		updateValue(width)
		if !form.syncing && slices.Contains(d.groups, group) {
			d.setGroupNameWidth(group, float32(width))
		}
	}
	form.slider.OnChangeEnded = func(float64) {
		if form.syncing {
			return
		}
		d.saveGroupNameWidth(group, previous)
		previous = d.groupNameWidth(group)
		form.syncing = true
		form.slider.SetValue(float64(previous))
		form.syncing = false
	}
	form.popup.Resize(fyne.NewSize(min(300, d.window.Canvas().Size().Width-24), content.MinSize().Height))
	for _, row := range d.tabs.renderer.rows {
		if row.id == group {
			d.positionShortcut(form.popup, row.label)
			return
		}
	}
}

// The name takes only the space it needs, up to the chosen cap. The row still
// reserves room for tabs and actions when a wider cap cannot fit the window.
type groupRowLayout struct{}

func (groupRowLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	height := max(objects[0].MinSize().Height, objects[2].MinSize().Height, objects[3].MinSize().Height)
	return fyne.NewSize(min(64, objects[0].MinSize().Width)+6+48+objects[3].MinSize().Width+16, height)
}

func (groupRowLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	label, handle, scroll, actions := objects[0], objects[1], objects[2], objects[3]
	actionWidth := actions.MinSize().Width
	nameWidth := min(label.MinSize().Width, max(0, size.Width-actionWidth-48-6-16))
	label.Move(fyne.NewPos(0, 0))
	label.Resize(fyne.NewSize(nameWidth, size.Height))
	handle.Move(fyne.NewPos(nameWidth, 0))
	handle.Resize(fyne.NewSize(6, size.Height))
	scroll.Move(fyne.NewPos(nameWidth+6+8, 0))
	scroll.Resize(fyne.NewSize(max(0, size.Width-nameWidth-6-16-actionWidth), size.Height))
	actions.Move(fyne.NewPos(size.Width-actionWidth, 0))
	actions.Resize(fyne.NewSize(actionWidth, size.Height))
}

type groupWidthHandle struct {
	widget.BaseWidget
	hovered bool
	drag    func(float32)
	ended   func()
	reset   func()
}

func newGroupWidthHandle(drag func(float32), ended, reset func()) *groupWidthHandle {
	handle := &groupWidthHandle{drag: drag, ended: ended, reset: reset}
	handle.ExtendBaseWidget(handle)
	return handle
}

func (h *groupWidthHandle) AccessibilityLabel() string {
	return "Arrastar o limite do nome · duplo clique restaura"
}
func (h *groupWidthHandle) Cursor() fynedesktop.Cursor         { return fynedesktop.HResizeCursor }
func (h *groupWidthHandle) MouseMoved(*fynedesktop.MouseEvent) {}

func (h *groupWidthHandle) Dragged(event *fyne.DragEvent) {
	if h.drag != nil {
		h.drag(event.Dragged.DX)
	}
}

func (h *groupWidthHandle) DragEnd() {
	if h.ended != nil {
		h.ended()
	}
}

func (h *groupWidthHandle) DoubleTapped(*fyne.PointEvent) {
	if h.reset != nil {
		h.reset()
	}
}

func (h *groupWidthHandle) MouseIn(*fynedesktop.MouseEvent) { h.hovered = true; h.Refresh() }
func (h *groupWidthHandle) MouseOut()                       { h.hovered = false; h.Refresh() }

func (h *groupWidthHandle) CreateRenderer() fyne.WidgetRenderer {
	line := canvas.NewRectangle(color.Transparent)
	line.CornerRadius = 1
	return &groupWidthRenderer{handle: h, line: line}
}

type groupWidthRenderer struct {
	handle *groupWidthHandle
	line   *canvas.Rectangle
}

func (r *groupWidthRenderer) Layout(size fyne.Size) {
	r.line.Move(fyne.NewPos((size.Width-2)/2, 8))
	r.line.Resize(fyne.NewSize(2, max(0, size.Height-16)))
}
func (r *groupWidthRenderer) MinSize() fyne.Size           { return fyne.NewSize(6, 24) }
func (r *groupWidthRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.line} }
func (r *groupWidthRenderer) Destroy()                     {}
func (r *groupWidthRenderer) Refresh() {
	r.line.FillColor = color.Transparent
	if r.handle.hovered {
		r.line.FillColor = r.handle.Theme().Color(mutedColor, theme.VariantDark)
	}
	r.Layout(r.handle.Size())
	r.line.Refresh()
	canvas.Refresh(r.handle)
}
