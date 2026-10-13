package desktop

import "fyne.io/fyne/v2"

// settingsResizeLayout observes the canvas without recreating the settings
// pages or their editors. The existing dialog keeps focus and unsaved values.
type settingsResizeLayout struct {
	desktop *desktop
}

func (l *settingsResizeLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, object := range objects {
		object.Move(fyne.NewPos(0, 0))
		object.Resize(size)
	}
	l.desktop.syncSettingsDialogSize()
}

func (*settingsResizeLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var size fyne.Size
	for _, object := range objects {
		size = size.Max(object.MinSize())
	}
	return size
}

func (d *desktop) syncSettingsDialogSize() {
	if form := d.settingsForm; form != nil && form.dialog != nil {
		size := dialogSize(d.window)
		if form.dialogSize != size {
			form.dialogSize = size
			form.dialog.Resize(size)
		}
	}
}
