package desktop

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ai-talker/internal/config"
	"ai-talker/internal/platform"
	"ai-talker/internal/soundboard"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	fynedesktop "fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type soundEffectPlayback struct {
	id     string
	player audioPlayer
	cancel context.CancelFunc
}

// prepareSoundboard starts at application startup. Production clicks never
// launch FFmpeg, open a file, create an MCI device, or wait behind old effects.
func (d *desktop) prepareSoundboard() {
	if d.deps.newSoundEngine == nil || d.soundboardLoading || d.soundboard != nil {
		return
	}
	d.soundboardLoading = true
	d.soundboardButton.Disable()
	d.soundboardButton.hint.text = uiText("Preparando painel de sons…")
	var board *soundboard.Board
	var unavailable []string
	engine := d.deps.newSoundEngine()
	d.operation(func() error {
		var err error
		board, err = soundboard.Load(d.directory)
		if err != nil {
			return err
		}
		if err := engine.Start(d.ctx); err != nil {
			return err
		}
		for _, button := range board.Buttons() {
			path, err := board.Path(button.ID)
			if err == nil {
				err = engine.Prepare(d.ctx, button.ID, path)
			}
			if err != nil {
				if !button.Builtin && d.ctx.Err() == nil {
					unavailable = append(unavailable, button.Name+": "+shortMessage(err.Error()))
					continue
				}
				engine.Close()
				return err
			}
		}
		if d.ctx.Err() != nil {
			engine.Close()
			return d.ctx.Err()
		}
		return nil
	}, func(err error) {
		d.soundboardLoading = false
		d.soundboardButton.Enable()
		d.soundboardButton.hint.text = uiText("Abrir painel de sons")
		if err != nil {
			// The rest of the app remains available; opening the panel displays
			// the preparation error and allows importing/removing invalid sounds.
			d.soundboard = board
			d.status.SetText(uiText("Não foi possível preparar o painel de sons: ") + shortMessage(err.Error()))
			return
		}
		d.soundboard, d.soundEngine = board, engine
		if len(unavailable) > 0 {
			d.status.SetText(uiText("Alguns sons não puderam ser preparados: ") + strings.Join(unavailable, "; "))
		}
		d.workers.Go(func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-d.ctx.Done():
					engine.Close()
					return
				case <-ticker.C:
					if !engine.Active() {
						d.post(func() {
							if d.soundEngine == engine && d.soundPlayback != nil && !engine.Active() {
								d.soundPlayback = nil
								if err := engine.Err(); err != nil {
									d.setSoundboardStatus(uiText("Não foi possível tocar o som: ") + shortMessage(err.Error()))
								} else {
									d.setSoundboardStatus(uiText("Pronto."))
								}
							}
						})
					}
				}
			}
		})
	})
}

func (d *desktop) openSoundboard() {
	if d.closing || d.soundboardLoading {
		return
	}
	if d.soundboardWindow != nil {
		d.soundboardWindow.Show()
		d.soundboardWindow.RequestFocus()
		return
	}
	if d.soundboard != nil {
		d.showSoundboardWindow()
		return
	}
	d.soundboardLoading = true
	d.soundboardButton.Disable()
	var board *soundboard.Board
	d.operation(func() error {
		var err error
		board, err = soundboard.Load(d.directory)
		return err
	}, func(err error) {
		d.soundboardLoading = false
		d.soundboardButton.Enable()
		if err != nil {
			d.showError(uiText("Não foi possível carregar o painel de sons: ")+err.Error(), "")
			return
		}
		d.soundboard = board
		d.showSoundboardWindow()
	})
}

func (d *desktop) showSoundboardWindow() {
	if d.soundboard == nil || d.closing {
		return
	}
	var window fyne.Window
	if runtime.GOOS == "windows" {
		if driver, ok := d.application.Driver().(fynedesktop.Driver); ok {
			window = driver.CreateSplashWindow()
		}
	}
	if window == nil {
		window = d.application.NewWindow(uiText("Painel de sons"))
	}
	window.SetTitle(uiText("Painel de sons"))
	d.soundboardWindow = window
	window.SetIcon(d.window.Icon())
	window.Resize(fyne.NewSize(580, 420))
	window.SetCloseIntercept(d.guard(func() {
		d.closeSoundboardWindow(window)
	}))
	window.SetOnClosed(d.guard(func() {
		if d.soundboardWindow == window {
			d.soundboardWindow = nil
			d.stopSoundboardPlayback()
			d.soundboardVolume = nil
			d.soundboardStatus = nil
			if d.soundboardFrameCancel != nil {
				d.soundboardFrameCancel()
				d.soundboardFrameCancel = nil
			}
		}
	}))
	d.renderSoundboard()
	window.Show()
	window.RequestFocus()
	d.watchSoundboardWindow(window)
}

func (d *desktop) renderSoundboard() {
	window, board := d.soundboardWindow, d.soundboard
	if window == nil || board == nil {
		return
	}
	window.SetTitle(uiText("Painel de sons"))
	hints := newHintLayer(window.Canvas())
	title := widget.NewLabel(uiText("Painel de sons"))
	title.TextStyle.Bold = true
	status := widget.NewLabel(
		uiText("Um efeito por vez. Outro clique substitui o som; o mesmo botão reinicia. " +
			"Para gravar os efeitos, use Sistema ou Equipe e capture a saída da reprodução."),
	)
	status.Wrapping = fyne.TextWrapWord
	d.soundboardStatus = widget.NewLabel(uiText("Pronto."))
	if d.soundboardPreparing {
		d.soundboardStatus.SetText(uiText("Preparando sons…"))
	} else if d.soundEngine != nil {
		ready := 0
		for _, button := range board.Buttons() {
			if d.soundEngine.Ready(button.ID) {
				ready++
			}
		}
		if ready < len(board.Buttons()) {
			d.soundboardStatus.SetText(uiTextf("Preparados: %d de %d sons.", ready, len(board.Buttons())))
		}
	}
	d.soundboardStatus.Wrapping = fyne.TextWrapWord

	var rows []fyne.CanvasObject
	for _, button := range board.Buttons() {
		item := button
		name := soundButtonName(item)
		play := widget.NewButton(name, func() {
			d.playSoundEffect(item)
		})
		if _, translate := soundboard.DefaultName(item); !translate {
			d.preserveUIText(play)
		}
		if d.soundboardPreparing || d.deps.newSoundEngine != nil &&
			(d.soundEngine == nil || !d.soundEngine.Ready(item.ID)) {
			play.Disable()
		}
		var menuButton *actionButton
		menuButton = d.iconButton(uiTextf("Opções de %s", name), theme.MoreVerticalIcon(), func() {
			d.showSoundButtonMenu(item, menuButton)
		})
		menuButton.hint.layer = hints
		menuButton.Importance = widget.LowImportance
		options := container.NewBorder(nil, nil, nil, menuButton, layout.NewSpacer())
		rows = append(rows, container.NewBorder(nil, options, nil, nil, play))
	}
	list := container.NewGridWrap(fyne.NewSize(180, 90), rows...)
	scroll := container.NewVScroll(list)
	scroll.SetMinSize(fyne.NewSize(200, 160))

	volume := widget.NewSlider(0, 100)
	volume.Step = 1
	volume.SetValue(float64(board.Volume()))
	value := widget.NewLabel(fmt.Sprintf("%d%%", board.Volume()))
	syncing := false
	volume.OnChanged = func(updated float64) {
		if syncing {
			return
		}
		requested := int(updated)
		value.SetText(fmt.Sprintf("%d%%", requested))
		if d.soundPlayback != nil {
			d.setSoundEffectVolume(requested)
		}
	}
	volume.OnChangeEnded = func(updated float64) {
		requested := int(updated)
		volume.Disable()
		d.operation(func() error {
			return board.SetVolume(requested)
		}, func(err error) {
			saved := board.Volume()
			syncing = true
			volume.SetValue(float64(saved))
			syncing = false
			value.SetText(fmt.Sprintf("%d%%", saved))
			volume.Enable()
			if d.soundPlayback != nil {
				d.setSoundEffectVolume(saved)
			}
			if err != nil {
				d.setSoundboardStatus(uiText("Não foi possível salvar o volume: ") + err.Error())
			}
		})
	}
	d.soundboardVolume = volume
	volumeRow := container.NewBorder(nil, nil, widget.NewLabel(uiText("Volume")), value, volume)

	add := widget.NewButtonWithIcon(uiText("Adicionar som WAV ou MP3"), theme.ContentAddIcon(), d.addSoundButton)
	stop := widget.NewButtonWithIcon(uiText("Parar som"), theme.MediaStopIcon(), d.stopSoundboardPlayback)
	buttons := container.NewHBox(add, stop)
	minimize := d.iconButton(uiText("Minimizar janela"), theme.WindowMinimizeIcon(), func() {
		d.soundboardWindowAction(platform.AuxiliaryMinimize)
	})
	maximize := d.iconButton(uiText("Maximizar ou restaurar janela"), theme.WindowMaximizeIcon(), func() {
		d.soundboardWindowAction(platform.AuxiliaryToggleMaximized)
	})
	closeButton := d.iconButton(uiText("Fechar painel de sons"), theme.WindowCloseIcon(), func() {
		d.closeSoundboardWindow(window)
	})
	for _, button := range []*actionButton{minimize, maximize, closeButton} {
		button.hint.layer = hints
	}
	headerTools := container.NewHBox(minimize, maximize, closeButton)
	header := container.NewThemeOverride(container.NewBorder(nil, nil, nil, headerTools, title), headerTheme{Theme: d.palette})
	content := container.NewBorder(
		container.NewVBox(header, status),
		container.NewVBox(d.soundboardStatus, volumeRow, buttons),
		nil, nil, scroll,
	)
	window.SetContent(container.NewStack(inset(content, 12, 10), hints.content))
}

func (d *desktop) playSoundEffect(button soundboard.Button) {
	if d.closing {
		return
	}
	if d.deps.newSoundEngine != nil && d.soundEngine == nil {
		d.setSoundboardStatus(uiText("O painel de sons ainda não está preparado."))
		return
	}
	if d.soundEngine != nil {
		d.soundEngine.SetVolume(d.soundboard.Volume())
		if err := d.soundEngine.Play(button.ID); err != nil {
			d.setSoundboardStatus(uiText("Não foi possível tocar o som: ") + shortMessage(err.Error()))
			return
		}
		d.soundPlayback = &soundEffectPlayback{id: button.ID}
		d.setSoundboardStatus(uiTextf("Tocando: %s", soundButtonName(button)))
		return
	}
	if d.closing || d.soundboard == nil || d.deps.newPlayer == nil {
		return
	}
	path, err := d.soundboard.Path(button.ID)
	if err != nil {
		d.setSoundboardStatus(uiText("Não foi possível abrir o som: ") + err.Error())
		return
	}
	player := d.deps.newPlayer(d.directory)
	if player == nil {
		d.setSoundboardStatus(uiText("A reprodução de áudio não está disponível."))
		return
	}
	d.stopSoundboardPlayback()
	ctx, cancel := context.WithCancel(d.ctx)
	session := &soundEffectPlayback{id: button.ID, player: player, cancel: cancel}
	d.soundPlayback = session
	volume := d.soundboard.Volume()
	player.SetVolume(volume)
	player.SetSpeed(1)
	d.setSoundboardStatus(uiTextf("Tocando: %s", soundButtonName(button)))
	d.workers.Go(func() {
		err := callOperation(func() error {
			return d.withSoundPlaybackSlot(ctx, func() error { return player.Run(ctx, path) })
		})
		cancel()
		d.post(func() {
			if d.soundPlayback != session {
				return
			}
			d.soundPlayback = nil
			if err != nil && !errors.Is(err, context.Canceled) {
				d.setSoundboardStatus(uiText("Não foi possível tocar o som: ") + shortMessage(err.Error()))
			} else {
				d.setSoundboardStatus(uiText("Pronto."))
			}
		})
	})
}

func (d *desktop) withSoundPlaybackSlot(ctx context.Context, work func() error) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case d.soundPlaybackSlot <- struct{}{}:
	}
	defer func() { <-d.soundPlaybackSlot }()
	if err := ctx.Err(); err != nil {
		return err
	}
	return work()
}

func (d *desktop) stopSoundboardPlayback() {
	if d.soundEngine != nil {
		d.soundEngine.Stop()
	}
	if session := d.soundPlayback; session != nil {
		d.soundPlayback = nil
		if session.cancel != nil {
			session.cancel()
		}
	}
	if d.soundboardStatus != nil {
		d.soundboardStatus.SetText(uiText("Pronto."))
	}
}

func (d *desktop) setSoundboardStatus(message string) {
	if d.soundboardStatus != nil {
		d.soundboardStatus.SetText(message)
	}
}

func (d *desktop) showSoundButtonMenu(button soundboard.Button, anchor fyne.CanvasObject) {
	items := []*fyne.MenuItem{
		fyne.NewMenuItem(uiText("Renomear"), d.guard(func() { d.renameSoundButton(button) })),
	}
	if !button.Builtin {
		items = append(items, fyne.NewMenuItem(uiText("Remover"), d.guard(func() { d.confirmRemoveSoundButton(button) })))
	}
	menu := widget.NewPopUpMenu(fyne.NewMenu(soundButtonName(button), items...), d.soundboardWindow.Canvas())
	menu.ShowAtRelativePosition(fyne.NewPos(0, anchor.Size().Height), anchor)
}

func (d *desktop) addSoundButton() {
	if d.soundboard == nil || d.deps.chooseFile == nil {
		d.setSoundboardStatus(uiText("A seleção de arquivos não está disponível."))
		return
	}
	d.selectFile(platform.FileDialogOptions{
		Title:  uiText("Adicionar som ao painel de sons"),
		Folder: filepath.Join(d.directory, "soundboard"),
		Filters: []platform.FileFilter{
			{Name: uiText("Áudio WAV ou MP3"), Pattern: "*.wav;*.mp3"},
			{Name: uiText("Todos os arquivos"), Pattern: "*.*"},
		},
	}, func(path string, err error) {
		if err != nil {
			d.setSoundboardStatus(uiText("Não foi possível escolher o som: ") + err.Error())
			return
		}
		if path == "" {
			return
		}
		name := strings.TrimSpace(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
		if name == "" {
			name = uiText("Novo som")
		}
		d.requestSoundButtonName(uiText("Adicionar som"), name, func(value string) {
			d.mutateSoundboard(func(board *soundboard.Board) error {
				_, err := board.Add(path, value)
				return err
			})
		})
	})
}

func (d *desktop) renameSoundButton(button soundboard.Button) {
	d.requestSoundButtonName(uiText("Renomear som"), soundButtonName(button), func(name string) {
		d.mutateSoundboard(func(board *soundboard.Board) error {
			return board.Rename(button.ID, name)
		})
	})
}

func (d *desktop) requestSoundButtonName(title, initial string, complete func(string)) {
	window := d.soundboardWindow
	if window == nil {
		return
	}
	entry := dialog.NewEntryDialog(title, uiText("Nome exibido no botão"), func(value string) {
		d.guard(func() {
			if strings.TrimSpace(value) != "" {
				complete(value)
			}
		})()
	}, window)
	entry.SetText(initial)
	entry.Show()
	entry.Resize(dialogSize(window))
}

func (d *desktop) confirmRemoveSoundButton(button soundboard.Button) {
	if d.soundboardWindow == nil {
		return
	}
	confirmation := dialog.NewConfirm(
		uiText("Remover som"),
		uiTextf("Remover “%s” do painel de sons? A cópia interna do arquivo também será removida.", soundButtonName(button)),
		func(confirmed bool) {
			if confirmed {
				d.guard(func() {
					engine := d.soundEngine
					d.mutateSoundboard(func(board *soundboard.Board) error {
						if engine != nil {
							engine.Remove(button.ID)
						}
						return board.Remove(button.ID)
					})
				})()
			}
		},
		d.soundboardWindow,
	)
	confirmation.SetConfirmText(uiText("Remover"))
	confirmation.Show()
}

func (d *desktop) mutateSoundboard(mutate func(*soundboard.Board) error) {
	board := d.soundboard
	if board == nil {
		return
	}
	d.soundboardPreparing = true
	d.setSoundboardStatus(uiText("Preparando sons…"))
	d.renderSoundboard()
	engine := d.soundEngine
	var unavailable []string
	d.operation(func() error {
		if err := mutate(board); err != nil {
			return err
		}
		if engine != nil {
			for _, button := range board.Buttons() {
				path, err := board.Path(button.ID)
				if err == nil {
					err = engine.Prepare(d.ctx, button.ID, path)
				}
				if err != nil {
					if !button.Builtin && d.ctx.Err() == nil {
						unavailable = append(unavailable, button.Name+": "+shortMessage(err.Error()))
						continue
					}
					return err
				}
			}
		}
		return nil
	}, func(err error) {
		d.soundboardPreparing = false
		d.renderSoundboard()
		if err != nil {
			d.setSoundboardStatus(err.Error())
			return
		}
		if len(unavailable) > 0 {
			d.setSoundboardStatus(uiText("Alguns sons não puderam ser preparados: ") + strings.Join(unavailable, "; "))
			return
		}
		d.setSoundboardStatus(uiText("Painel de sons atualizado."))
	})
}

func soundButtonName(button soundboard.Button) string {
	if name, translate := soundboard.DefaultName(button); translate {
		return uiText(name)
	}
	return button.Name
}

func (d *desktop) setSoundEffectVolume(volume int) {
	if d.soundEngine != nil {
		d.soundEngine.SetVolume(volume)
	} else if d.soundPlayback != nil && d.soundPlayback.player != nil {
		d.soundPlayback.player.SetVolume(volume)
	}
}

func (d *desktop) soundboardWindowAction(action func(string) error) {
	window := d.soundboardWindow
	if window == nil {
		return
	}
	title := window.Title()
	d.operation(func() error { return action(title) }, func(err error) {
		if err != nil {
			d.setSoundboardStatus(shortMessage(err.Error()))
		}
	})
}

func (d *desktop) saveSoundboardPosition(title string) {
	board := d.soundboard
	if board == nil {
		return
	}
	position, err := platform.AuxiliaryWindowGeometry(title)
	if err == nil && !position.Maximized && !position.Minimized {
		d.operation(func() error {
			return board.SetPosition(soundboard.Position{X: position.X, Y: position.Y})
		}, func(error) {})
	}
}

func (d *desktop) closeSoundboardWindow(window fyne.Window) {
	if window == nil {
		return
	}
	d.saveSoundboardPosition(window.Title())
	window.Close()
}

func (d *desktop) watchSoundboardWindow(window fyne.Window) {
	if runtime.GOOS != "windows" || d.deps.newSoundEngine == nil {
		return
	}
	ctx, cancel := context.WithCancel(d.ctx)
	d.soundboardFrameCancel = cancel
	board := d.soundboard
	scale := window.Canvas().Scale()
	if scale <= 0 {
		scale = 1
	}
	pixels := func(value float32) int { return int(math.Round(float64(value * scale))) }
	header := platform.WindowHeader{Top: pixels(10), Bottom: pixels(44), ControlsWidth: pixels(150)}
	dark := d.settings.Appearance.Theme != config.ThemeLight
	background := paletteFor(d.settings.Appearance.Theme).background
	title := window.Title()
	d.workers.Go(func() {
		// Native creation follows Show asynchronously in GLFW. Retry only
		// this process's exact auxiliary title until it exists.
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		installed := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if ctx.Err() != nil {
					return
				}
				activeTitle := title
				if _, err := platform.AuxiliaryWindowGeometry(activeTitle); err != nil {
					if activeTitle == "Painel de sons" {
						activeTitle = "Soundboard"
					} else {
						activeTitle = "Painel de sons"
					}
				}
				if !installed {
					if err := platform.AuxiliaryTitlebar(activeTitle, dark, background, header); err != nil {
						continue
					}
					if position := board.Position(); position != nil {
						_ = platform.PositionAuxiliaryWindow(activeTitle, position.X, position.Y)
					}
					installed = true
				}
				position, err := platform.AuxiliaryWindowGeometry(activeTitle)
				if err == nil && !position.Maximized && !position.Minimized {
					_ = board.SetPosition(soundboard.Position{X: position.X, Y: position.Y})
				}
			}
		}
	})
}
