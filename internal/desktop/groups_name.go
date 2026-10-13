package desktop

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// A long group name must not expand the minimum width or hide the row's actions.
type groupLabel struct {
	widget.Label
	secondary func(*fyne.PointEvent)
	limit     func() float32
	dragged   func(*fyne.DragEvent)
	dragEnd   func()
}

func (l *groupLabel) Dragged(event *fyne.DragEvent) {
	if l.dragged != nil {
		l.dragged(event)
	}
}

func (l *groupLabel) DragEnd() {
	if l.dragEnd != nil {
		l.dragEnd()
	}
}

func (l *groupLabel) TappedSecondary(event *fyne.PointEvent) {
	if l.secondary != nil {
		l.secondary(event)
	}
}

func (l *groupLabel) MinSize() fyne.Size {
	size := l.Label.MinSize()
	limit := defaultGroupNameWidth
	if l.limit != nil {
		limit = l.limit()
	}
	// Fyne's ellipsis layout converts the available text width to an integer.
	// Round up so a naturally sized short name keeps its last character.
	size.Width = min(limit, l.naturalWidth())
	return size
}

func (l *groupLabel) naturalWidth() float32 {
	text := fyne.MeasureText(l.Text, l.Theme().Size(theme.SizeNameText), l.TextStyle)
	return float32(math.Ceil(float64(text.Width))) + 2*l.Theme().Size(theme.SizeNameInnerPadding)
}

func (l *groupLabel) Resize(size fyne.Size) {
	truncation := fyne.TextTruncateEllipsis
	if size.Width >= l.naturalWidth() {
		truncation = fyne.TextTruncateOff
	}
	changed := l.Truncation != truncation
	l.Truncation = truncation
	l.Label.Resize(size)
	if changed {
		l.Label.Refresh()
	}
}

func validateGroupName(name string) error {
	if name == "" || name != strings.TrimSpace(name) || !utf8.ValidString(name) ||
		utf8.RuneCountInString(name) > 40 || strings.ContainsAny(name, "\r\n\t\x00") {
		return errors.New("informe um nome de 1 a 40 caracteres, em uma linha")
	}
	return nil
}

func (d *desktop) groupContextMenu(group uint64, event *fyne.PointEvent) {
	if !slices.Contains(d.groups, group) || d.tabsDialog || d.discardDialog {
		return
	}
	menu := widget.NewPopUpMenu(d.groupTabsMenuFor(group), d.window.Canvas())
	menu.ShowAtPosition(event.AbsolutePosition)
}

func (d *desktop) openRenameGroup(group uint64) {
	if !slices.Contains(d.groups, group) || d.tabsDialog || d.discardDialog {
		return
	}
	entry := widget.NewEntry()
	entry.SetText(d.groupName(group))
	entry.SetPlaceHolder(uiText("Nome do grupo"))
	errorLabel := widget.NewLabel("")
	errorLabel.Importance = widget.DangerImportance
	errorLabel.Wrapping = fyne.TextWrapWord
	errorLabel.Hide()
	var form *dialog.CustomDialog
	save := widget.NewButton(uiText("Renomear"), d.guard(func() {
		if err := d.renameGroup(group, entry.Text); err != nil {
			errorLabel.SetText(err.Error())
			errorLabel.Show()
			return
		}
		form.Hide()
	}))
	save.Importance = widget.HighImportance
	form = dialog.NewCustom("Renomear "+d.groupName(group), uiText("Cancelar"),
		container.NewVBox(entry, errorLabel, save), d.window)
	form.Resize(fyne.NewSize(360, 180))
	form.Show()
	d.window.Canvas().Focus(entry)
}

func (d *desktop) renameGroup(group uint64, name string) error {
	if !slices.Contains(d.groups, group) {
		return errors.New("este grupo já foi fechado")
	}
	name = strings.TrimSpace(name)
	if err := validateGroupName(name); err != nil {
		return err
	}
	previous, renamed := d.groupNames[group]
	automatic := d.groupAutomaticNames[group]
	d.groupNames[group] = name
	delete(d.groupAutomaticNames, group)
	if err := d.saveWorkspace(); err != nil {
		if renamed {
			d.groupNames[group] = previous
		} else {
			delete(d.groupNames, group)
		}
		if automatic != "" {
			d.groupAutomaticNames[group] = automatic
		}
		return fmt.Errorf("não foi possível salvar o nome do grupo: %w", err)
	}
	d.tabs.Refresh()
	d.syncControls()
	return nil
}
