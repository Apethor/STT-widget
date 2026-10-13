package desktop

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"ai-talker/internal/screenshot"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	fynedesktop "fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type screenshotTool uint8

const (
	screenshotBrush screenshotTool = iota
	screenshotEraser
	screenshotText
)

// Each editor owns its image and path; recording, tab selection and other
// screenshot editors never redirect its saves or deletion.
type screenshotEditor struct {
	desktop       *desktop
	window        fyne.Window
	document      *screenshot.Document
	association   *screenshotAssociation
	path          string
	surface       *screenshotSurface
	hints         *hintLayer
	tool          screenshotTool
	ink           color.NRGBA
	width         float64
	font          string
	fontSize      float64
	savedRevision uint64
	busy          bool
	closed        bool
	textDialog    bool
	status        *widget.Label
	brush         *actionButton
	eraser        *actionButton
	text          *actionButton
	colorButton   *actionButton
	save          *actionButton
	trash         *actionButton
	undo          *actionButton
	redo          *actionButton
	copyPath      *actionButton
	zoom          *widget.Select
	zoomButton    *actionButton
	zoomFit       bool
	zoomPercent   float64
	viewport      *container.Scroll
}

func newScreenshotEditor(d *desktop, path string, document *screenshot.Document) *screenshotEditor {
	e := &screenshotEditor{
		desktop: d, window: d.application.NewWindow("Screenshot · " + filepath.Base(path)),
		document: document, path: path, ink: color.NRGBA{R: 166, G: 92, B: 255, A: 255},
		width: 4, font: screenshot.FontSans, fontSize: 24, zoomFit: true, zoomPercent: 100,
	}
	e.window.SetIcon(d.application.Icon())
	e.hints = newHintLayer(e.window.Canvas())
	e.surface = newScreenshotSurface(e)
	e.status = widget.NewLabel("")
	e.status.Wrapping = fyne.TextWrapWord
	e.brush = e.button("Pincel · desenhar", brushIcon, func() { e.selectTool(screenshotBrush) })
	e.eraser = e.button("Borracha · remover marcações", eraserIcon, func() { e.selectTool(screenshotEraser) })
	e.text = e.button("Texto · clicar na imagem para escrever", textAnnotationIcon, func() { e.selectTool(screenshotText) })
	e.colorButton = e.button("Escolher cor", theme.ColorPaletteIcon(), e.chooseColor)
	e.colorButton.dotColor = e.ink
	e.undo = e.button("Desfazer marcação", theme.ContentUndoIcon(), func() { e.document.Undo(); e.refresh() })
	e.redo = e.button("Refazer marcação", theme.ContentRedoIcon(), func() { e.document.Redo(); e.refresh() })
	e.save = e.button("Salvar marcações no mesmo PNG", theme.DocumentSaveIcon(), e.saveImage)
	e.trash = e.button("Excluir screenshot e fechar", theme.DeleteIcon(), e.deleteImage)
	e.copyPath = e.button("Copiar caminho da screenshot", theme.ContentCopyIcon(), e.copyImagePath)
	e.copyPath.compact = true
	folder := e.button("Abrir pasta de screenshots", theme.FolderOpenIcon(), e.openFolder)
	closeButton := e.button("Fechar editor · manter última imagem salva", theme.WindowCloseIcon(), e.window.Close)
	zoomOptions := []string{"Ajustar à janela", "75%", "80%", "90%", "100%"}
	for percent := 110; percent <= 200; percent += 10 {
		zoomOptions = append(zoomOptions, strconv.Itoa(percent)+"%")
	}
	e.zoom = widget.NewSelect(zoomOptions, func(value string) {
		if value == "Ajustar à janela" {
			e.zoomFit = true
		} else {
			value = strings.TrimSuffix(value, "%")
			percent, err := strconv.ParseFloat(value, 64)
			if err != nil || percent < 75 || percent > 200 {
				return
			}
			e.zoomFit, e.zoomPercent = false, percent
		}
		if e.viewport != nil {
			e.viewport.Refresh()
		}
		e.surface.Refresh()
	})
	setUISelected(e.zoom, "Ajustar à janela")
	e.zoomButton = e.button("Zoom da imagem · altera somente a visualização", theme.SearchIcon(), func() {
		e.zoom.Tapped(&fyne.PointEvent{})
	})
	tools := container.NewHBox(e.brush, e.eraser, e.text, widget.NewSeparator(),
		e.colorButton, e.undo, e.redo, widget.NewSeparator(), e.save, e.trash, e.copyPath)
	viewTools := container.NewHBox(e.zoomButton, e.zoom, folder, closeButton)
	thickness := widget.NewSelect([]string{"2", "4", "8", "12", "20", "32"}, func(value string) {
		e.width, _ = strconv.ParseFloat(value, 64)
	})
	setUISelected(thickness, "4")
	font := widget.NewSelect([]string{screenshot.FontSans, screenshot.FontBold, screenshot.FontMono}, func(value string) { e.font = value })
	setUISelected(font, e.font)
	size := widget.NewSelect([]string{"12", "16", "20", "24", "32", "48", "64", "96"}, func(value string) {
		e.fontSize, _ = strconv.ParseFloat(value, 64)
	})
	setUISelected(size, "24")
	options := container.NewHBox(widget.NewLabel("Traço"), thickness, widget.NewLabel("Fonte"), font, widget.NewLabel("Texto"), size)
	header := container.NewVBox(
		container.NewBorder(nil, nil, nil, viewTools, container.NewHScroll(tools)),
		container.NewHScroll(options),
	)
	e.viewport = container.NewScroll(e.surface)
	body := container.NewBorder(header, e.status, nil, nil, e.viewport)
	e.window.SetContent(container.NewStack(body, e.hints.content))
	e.window.Resize(fyne.NewSize(1000, 720))
	e.window.CenterOnScreen()
	e.window.SetOnClosed(func() {
		e.closed = true
		e.hints.hide()
		delete(d.screenshotEditors, e)
	})
	e.window.Canvas().AddShortcut(&fynedesktop.CustomShortcut{KeyName: fyne.KeyS, Modifier: fyne.KeyModifierControl},
		func(fyne.Shortcut) { e.saveImage() })
	e.window.Canvas().AddShortcut(&fynedesktop.CustomShortcut{KeyName: fyne.KeyZ, Modifier: fyne.KeyModifierControl},
		func(fyne.Shortcut) {
			if !e.busy && !e.textDialog {
				e.document.Undo()
				e.refresh()
			}
		})
	e.window.Canvas().AddShortcut(&fynedesktop.CustomShortcut{KeyName: fyne.KeyY, Modifier: fyne.KeyModifierControl},
		func(fyne.Shortcut) {
			if !e.busy && !e.textDialog {
				e.document.Redo()
				e.refresh()
			}
		})
	e.refresh()
	return e
}

func (e *screenshotEditor) copyImagePath() {
	path, err := filepath.Abs(e.path)
	if err != nil {
		e.status.SetText("Não foi possível localizar o caminho: " + err.Error())
		return
	}
	e.desktop.operation(func() error { return e.desktop.deps.copy(path) }, func(err error) {
		if e.closed {
			return
		}
		if err != nil {
			e.status.SetText("Não foi possível copiar o caminho: " + err.Error())
			return
		}
		e.status.SetText("Caminho da screenshot copiado.")
	})
}

func (e *screenshotEditor) button(hint string, icon fyne.Resource, action func()) *actionButton {
	b := &actionButton{
		Button: widget.Button{Icon: icon, OnTapped: e.desktop.guard(func() {
			if !e.closed {
				action()
			}
		})},
		hint: toolHint{text: hint, layer: e.hints},
	}
	b.ExtendBaseWidget(b)
	return b
}

func (e *screenshotEditor) selectTool(tool screenshotTool) {
	if e.busy || e.textDialog {
		return
	}
	e.document.EndStroke()
	e.tool = tool
	e.refresh()
}

func (e *screenshotEditor) chooseColor() {
	picker := dialog.NewColorPicker("Cor da marcação", "Escolha a cor do pincel e do texto.", func(ink color.Color) {
		if ink == nil || e.closed {
			return
		}
		e.ink = color.NRGBAModel.Convert(ink).(color.NRGBA)
		e.ink.A = 255
		e.colorButton.dotColor = e.ink
		e.colorButton.Refresh()
	}, e.window)
	picker.SetColor(e.ink)
	picker.Show()
}

func (e *screenshotEditor) refresh() {
	e.surface.image.Image = e.document.Image()
	e.surface.Refresh()
	for tool, button := range map[screenshotTool]*actionButton{
		screenshotBrush: e.brush, screenshotEraser: e.eraser, screenshotText: e.text,
	} {
		button.Importance = widget.MediumImportance
		if tool == e.tool {
			button.Importance = widget.HighImportance
		}
		setEnabled(button, !e.busy)
		button.Refresh()
	}
	setEnabled(e.undo, !e.busy && e.document.CanUndo())
	setEnabled(e.redo, !e.busy && e.document.CanRedo())
	setEnabled(e.save, !e.busy && !e.textDialog)
	setEnabled(e.trash, !e.busy && !e.textDialog)
	state := "Salva"
	if e.document.Revision() != e.savedRevision {
		state = "Marcações pendentes · clique em Salvar"
	}
	if e.busy {
		state = "Salvando…"
	}
	e.status.SetText(state + " · " + e.path)
}

func (e *screenshotEditor) saveImage() {
	if e.closed || e.busy || e.textDialog {
		return
	}
	e.document.EndStroke()
	pixels, revision := e.document.Snapshot(), e.document.Revision()
	e.busy = true
	e.refresh()
	e.desktop.operation(func() error {
		return screenshot.Save(e.desktop.ctx, e.path, pixels)
	}, func(err error) {
		e.busy = false
		if e.closed {
			if err != nil {
				dialog.ShowError(err, e.desktop.window)
			}
			return
		}
		if err != nil {
			e.refresh()
			dialog.ShowError(fmt.Errorf("não foi possível salvar a screenshot: %w", err), e.window)
			return
		}
		e.savedRevision = revision
		e.refresh()
	})
}

func (e *screenshotEditor) deleteImage() {
	if e.closed || e.busy || e.textDialog {
		return
	}
	e.busy = true
	e.refresh()
	e.status.SetText("Excluindo screenshot…")
	e.desktop.operation(func() error {
		err := os.Remove(e.path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}, func(err error) {
		e.busy = false
		if err == nil {
			e.desktop.removeScreenshotReference(e.association, e.path)
		}
		if e.closed {
			if err != nil {
				dialog.ShowError(err, e.desktop.window)
			}
			return
		}
		if err != nil {
			e.refresh()
			dialog.ShowError(fmt.Errorf("não foi possível excluir a screenshot: %w", err), e.window)
			return
		}
		e.window.Close()
	})
}

func (e *screenshotEditor) openFolder() {
	e.desktop.operation(func() error { return e.desktop.deps.openFolder(filepath.Dir(e.path)) }, func(err error) {
		if err != nil && !e.closed {
			dialog.ShowError(err, e.window)
		}
	})
}

func (e *screenshotEditor) writeText(point screenshot.Point) {
	if e.textDialog {
		return
	}
	e.textDialog = true
	e.refresh()
	entry := widget.NewMultiLineEntry()
	entry.SetPlaceHolder(uiText("Digite o texto que aparecerá na imagem."))
	entry.SetMinRowsVisible(3)
	family, size, ink := e.font, e.fontSize, e.ink
	prompt := dialog.NewCustomConfirm("Texto na screenshot", "Inserir", "Cancelar", entry, func(accepted bool) {
		e.textDialog = false
		if e.closed {
			return
		}
		if accepted {
			if err := e.document.AddText(point, entry.Text, family, size, ink); err != nil {
				dialog.ShowError(err, e.window)
			}
		}
		e.refresh()
	}, e.window)
	prompt.Resize(fyne.NewSize(480, 260))
	prompt.Show()
	e.window.Canvas().Focus(entry)
}

type screenshotSurface struct {
	widget.BaseWidget
	editor  *screenshotEditor
	image   *canvas.Image
	drawing bool
}

func newScreenshotSurface(editor *screenshotEditor) *screenshotSurface {
	s := &screenshotSurface{editor: editor, image: canvas.NewImageFromImage(editor.document.Image())}
	s.image.FillMode = canvas.ImageFillContain
	s.image.ScaleMode = canvas.ImageScaleSmooth
	s.ExtendBaseWidget(s)
	return s
}

func (s *screenshotSurface) CreateRenderer() fyne.WidgetRenderer {
	return &screenshotSurfaceRenderer{surface: s}
}

func (s *screenshotSurface) Cursor() fynedesktop.Cursor {
	if s.editor.tool == screenshotText {
		return fynedesktop.TextCursor
	}
	return fynedesktop.CrosshairCursor
}

// imagePoint uses the same centered contain geometry as the renderer. All
// annotations remain in original pixel coordinates when the window resizes.
func (s *screenshotSurface) imagePoint(position fyne.Position, clamp bool) (screenshot.Point, bool) {
	bounds := s.editor.document.Image().Bounds()
	size := s.Size()
	scale := s.displayScale(size, bounds)
	if scale <= 0 {
		return screenshot.Point{}, false
	}
	offsetX := (float64(size.Width) - float64(bounds.Dx())*scale) / 2
	offsetY := (float64(size.Height) - float64(bounds.Dy())*scale) / 2
	x := (float64(position.X) - offsetX) / scale
	y := (float64(position.Y) - offsetY) / scale
	inside := x >= 0 && y >= 0 && x < float64(bounds.Dx()) && y < float64(bounds.Dy())
	if clamp {
		x, y = max(0, min(x, float64(bounds.Dx())-0.5)), max(0, min(y, float64(bounds.Dy())-0.5))
		inside = true
	}
	return screenshot.Point{X: x, Y: y}, inside
}

func (s *screenshotSurface) displayScale(size fyne.Size, bounds image.Rectangle) float64 {
	if !s.editor.zoomFit {
		return s.editor.zoomPercent / 100
	}
	return min(float64(size.Width)/float64(bounds.Dx()), float64(size.Height)/float64(bounds.Dy()))
}

func (s *screenshotSurface) MouseDown(event *fynedesktop.MouseEvent) {
	e := s.editor
	if e.busy || e.closed || e.textDialog || event.Button != fynedesktop.MouseButtonPrimary {
		return
	}
	point, inside := s.imagePoint(event.Position, false)
	if !inside {
		return
	}
	if e.tool == screenshotText {
		e.writeText(point)
		return
	}
	s.drawing = true
	e.document.BeginStroke(point, e.width, e.ink, e.tool == screenshotEraser)
	e.refresh()
}

func (s *screenshotSurface) MouseUp(event *fynedesktop.MouseEvent) {
	if event.Button == fynedesktop.MouseButtonPrimary {
		s.endStroke()
	}
}

func (s *screenshotSurface) Dragged(event *fyne.DragEvent) {
	if !s.drawing || s.editor.busy || s.editor.closed {
		return
	}
	point, inside := s.imagePoint(event.Position, true)
	if inside {
		s.editor.document.ExtendStroke(point)
		s.Refresh()
	}
}

func (s *screenshotSurface) DragEnd() { s.endStroke() }
func (s *screenshotSurface) endStroke() {
	if s.drawing {
		s.drawing = false
		s.editor.document.EndStroke()
		s.editor.refresh()
	}
}

type screenshotSurfaceRenderer struct{ surface *screenshotSurface }

func (r *screenshotSurfaceRenderer) Layout(size fyne.Size) {
	bounds := r.surface.editor.document.Image().Bounds()
	scale := r.surface.displayScale(size, bounds)
	imageSize := fyne.NewSize(float32(float64(bounds.Dx())*scale), float32(float64(bounds.Dy())*scale))
	r.surface.image.Move(fyne.NewPos((size.Width-imageSize.Width)/2, (size.Height-imageSize.Height)/2))
	r.surface.image.Resize(imageSize)
}

func (r *screenshotSurfaceRenderer) MinSize() fyne.Size {
	surface := r.surface
	if surface.editor.zoomFit {
		return fyne.NewSize(320, 240)
	}
	bounds := surface.editor.document.Image().Bounds()
	scale := surface.editor.zoomPercent / 100
	return fyne.NewSize(
		max(320, float32(float64(bounds.Dx())*scale)),
		max(240, float32(float64(bounds.Dy())*scale)),
	)
}
func (r *screenshotSurfaceRenderer) Refresh() {
	r.surface.image.Image = r.surface.editor.document.Image()
	r.Layout(r.surface.Size())
	r.surface.image.Refresh()
}
func (r *screenshotSurfaceRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.surface.image}
}
func (r *screenshotSurfaceRenderer) Destroy() {}
