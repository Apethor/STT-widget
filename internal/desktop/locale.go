package desktop

import (
	"path/filepath"
	"reflect"
	"sync"
	"weak"

	"ai-talker/internal/config"
	"ai-talker/internal/locale"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

func uiText(text string) string                   { return locale.Text(text) }
func uiTextf(format string, values ...any) string { return locale.Format(format, values...) }
func uiSource(text string) string                 { return locale.Source(text) }

var localizedChoices sync.Map
var preservedChoices sync.Map

func preserveUIChoices(selectWidget *widget.Select, values []string) {
	preserved := make(map[string]bool, len(values))
	for _, value := range values {
		preserved[value] = true
	}
	preservedChoices.Store(weak.Make(selectWidget), preserved)
}

func translateChoice(selectWidget *widget.Select, source string) string {
	if choices, exists := preservedChoices.Load(weak.Make(selectWidget)); exists && choices.(map[string]bool)[source] {
		return source
	}
	return uiText(source)
}

func uiSelected(selectWidget *widget.Select) string {
	return uiSelectedValue(selectWidget, selectWidget.Selected)
}

func uiSelectedValue(selectWidget *widget.Select, value string) string {
	if cached, exists := localizedChoices.Load(weak.Make(selectWidget)); exists {
		if source, exists := cached.(map[string]string)[value]; exists {
			return source
		}
	}
	return value
}

func translateChoices(selectWidget *widget.Select) bool {
	selected := uiSelected(selectWidget)
	sources := make(map[string]string, len(selectWidget.Options)*2)
	changed := false
	for index, option := range selectWidget.Options {
		source := uiSelectedValue(selectWidget, option)
		next := translateChoice(selectWidget, source)
		sources[source], sources[next] = source, source
		if option != next {
			selectWidget.Options[index], changed = next, true
		}
	}
	localizedChoices.Store(weak.Make(selectWidget), sources)
	next := translateChoice(selectWidget, selected)
	if next != selectWidget.Selected {
		selectWidget.Selected, changed = next, true
	}
	return changed
}

func setUISelected(selectWidget *widget.Select, source string) {
	translateChoices(selectWidget)
	callback := selectWidget.OnChanged
	selectWidget.OnChanged = nil
	previous := selectWidget.Selected
	selectWidget.SetSelected(translateChoice(selectWidget, source))
	selectWidget.OnChanged = callback
	if callback != nil && previous != selectWidget.Selected {
		callback(uiSelected(selectWidget))
	}
}

type localizedValue struct {
	source, rendered, language string
}

type interfaceLocalization struct {
	values    map[fyne.CanvasObject]map[string]localizedValue
	selectors map[*widget.Select]uintptr
	protected map[fyne.CanvasObject]bool
}

func newInterfaceLocalization(language string) *interfaceLocalization {
	locale.Set(language)
	return &interfaceLocalization{
		values:    make(map[fyne.CanvasObject]map[string]localizedValue),
		selectors: make(map[*widget.Select]uintptr),
		protected: make(map[fyne.CanvasObject]bool),
	}
}

// preserveUIText marks display content supplied by the person or a provider.
// Editors are always preserved; this helper covers labels/buttons used for
// custom names and reading views.
func (d *desktop) preserveUIText(object fyne.CanvasObject) {
	if d.localization != nil && object != nil {
		d.localization.protected[object] = true
	}
}

func (l *interfaceLocalization) text(object fyne.CanvasObject, field, value string) string {
	fields := l.values[object]
	if fields == nil {
		fields = make(map[string]localizedValue)
		l.values[object] = fields
	}
	stored := fields[field]
	if stored.rendered != value {
		stored.source = uiSource(value)
	}
	language := locale.Current()
	if stored.rendered == value && stored.language == language {
		return value
	}
	stored.rendered = locale.Translate(language, stored.source)
	stored.language = language
	fields[field] = stored
	return stored.rendered
}

// localizeInterface runs on the existing UI thread after an action or worker
// completion. It updates display fields in place, preserving focus, drafts,
// selections, tasks, editor contents and the current settings page.
func (d *desktop) localizeInterface() {
	l := d.localization
	if l == nil || d.window == nil {
		return
	}
	seen := make(map[fyne.CanvasObject]bool)
	var visit func(fyne.CanvasObject)
	visit = func(object fyne.CanvasObject) {
		if object == nil || seen[object] {
			return
		}
		seen[object] = true
		if l.protected[object] {
			return
		}
		l.object(object, visit)
	}
	visitDocument := func(doc *document) {
		if doc == nil {
			return
		}
		if doc.transcriptPreview != nil {
			l.protected[doc.transcriptPreview] = true
			visit(doc.transcriptPreview)
		}
		if doc.processPreview != nil {
			l.protected[doc.processPreview] = true
			visit(doc.processPreview)
		}
		if doc.editors != nil {
			visit(doc.editors)
		}
		if doc.transcriptPanel != nil {
			visit(doc.transcriptPanel)
		}
		if doc.processPanel != nil {
			visit(doc.processPanel)
		}
		if doc.voice != nil {
			if doc.voice.panel != nil {
				visit(doc.voice.panel)
			}
			if doc.voice.model != nil {
				visit(doc.voice.model)
			}
		}
	}
	for _, doc := range d.documents {
		visitDocument(doc)
	}
	// Hidden tabs and the complete closure sequence are still owned by the
	// workspace. Their content protection and selector identities survive Undo.
	for _, closed := range d.closedTabs {
		visitDocument(closed.document)
		visitDocument(closed.replacement)
		if closed.combined != nil {
			for _, doc := range closed.combined.documents {
				visitDocument(doc)
			}
		}
	}
	// The caption and its tooltip carry real filenames/paths. Its empty-state
	// label is translated by syncRecordingName before assigning the caption.
	l.protected[d.caption] = true
	visit(d.caption)
	for _, window := range d.application.Driver().AllWindows() {
		visit(window.Content())
		for _, overlay := range window.Canvas().Overlays().List() {
			visit(overlay)
		}
	}
	if d.soundboardWindow != nil {
		d.soundboardWindow.SetTitle(uiText("Painel de sons"))
	}
	for editor := range d.screenshotEditors {
		editor.window.SetTitle(uiText("Captura de tela") + " · " + filepath.Base(editor.path))
	}
	if form := d.settingsForm; form != nil && form.sections != nil {
		for _, page := range form.sections.pages {
			visit(page)
		}
		form.sections.tree.Refresh()
		form.sections.searchList.Refresh()
	}
	l.prune(seen)
	pruneLocalizedChoices()
}

func (l *interfaceLocalization) prune(live map[fyne.CanvasObject]bool) {
	for object := range l.values {
		if !live[object] {
			delete(l.values, object)
		}
	}
	for selectWidget := range l.selectors {
		if !live[selectWidget] {
			delete(l.selectors, selectWidget)
		}
	}
	for object := range l.protected {
		if !live[object] {
			delete(l.protected, object)
		}
	}
}

// These shared lookup tables contain weak widget keys and scalar data only.
// A disposed form cannot keep its widgets or callbacks alive through them.
func pruneLocalizedChoices() {
	for _, choices := range []*sync.Map{&localizedChoices, &preservedChoices} {
		choices.Range(func(key, _ any) bool {
			if key.(weak.Pointer[widget.Select]).Value() == nil {
				choices.Delete(key)
			}
			return true
		})
	}
}

func (l *interfaceLocalization) object(object fyne.CanvasObject, visit func(fyne.CanvasObject)) {
	switch value := object.(type) {
	case *fyne.Container:
		for _, child := range value.Objects {
			visit(child)
		}
	case *container.ThemeOverride:
		visit(value.Content)
	case *container.Scroll:
		visit(value.Content)
	case *container.Split:
		visit(value.Leading)
		visit(value.Trailing)
	case *widget.PopUp:
		visit(value.Content)
	case *documentTabs:
		if value.renderer != nil {
			visit(value.renderer.root)
		}
	case *themedPanel:
		visit(value.content)
	case *collapsiblePanel:
		visit(value.expanded)
		visit(value.column)
		visit(value.side)
	case *recorderSurface:
		visit(value.expanded)
		visit(value.collapsed)
	case *recordingSplitSurface:
		visit(value.split)
		visit(value.grip)
	case *groupLabel, *tabSelectButton:
		// These labels have explicit automatic-name metadata. Their own
		// renderers choose translated defaults and preserve custom names.
	case *themedText:
		text := l.text(object, "text", value.Text)
		if value.Text != text {
			value.Text = text
			value.Refresh()
		}
		if value.hint != nil {
			value.hint.text = l.text(object, "hint", value.hint.text)
		}
	case *actionButton:
		text := l.text(object, "text", value.Text)
		if value.Text != text {
			value.SetText(text)
		}
		value.hint.text = l.text(object, "hint", value.hint.text)
	case *heightHandle:
		value.hint.text = l.text(object, "hint", value.hint.text)
	case *qualityIndicator:
		value.hint.text = l.text(object, "hint", value.hint.text)
	case *connectionDot:
		value.hint.text = l.text(object, "hint", value.hint.text)
	case *widget.Label:
		text := l.text(object, "text", value.Text)
		if text != value.Text {
			value.SetText(text)
		}
	case *widget.Button:
		text := l.text(object, "text", value.Text)
		if text != value.Text {
			value.SetText(text)
		}
	case *widget.Check:
		text := l.text(object, "text", value.Text)
		if text != value.Text {
			value.Text = text
			value.Refresh()
		}
	case *widget.Hyperlink:
		text := l.text(object, "text", value.Text)
		if text != value.Text {
			value.SetText(text)
		}
	case *widget.Select:
		placeholder := l.text(object, "placeholder", value.PlaceHolder)
		if placeholder != value.PlaceHolder {
			value.PlaceHolder = placeholder
			value.Refresh()
		}
		l.selector(value)
	case *widget.SelectEntry:
		// Suggestions may include user-defined profile names; entry text is
		// never replaced. Only its fixed application placeholder is translated.
		text := l.text(object, "placeholder", value.PlaceHolder)
		if text != value.PlaceHolder {
			value.SetPlaceHolder(text)
		}
	case *widget.Entry:
		text := l.text(object, "placeholder", value.PlaceHolder)
		if text != value.PlaceHolder {
			value.SetPlaceHolder(text)
		}
	case *widget.RichText:
		changed := false
		for index, segment := range value.Segments {
			if text, ok := segment.(*widget.TextSegment); ok {
				next := l.text(object, uiTextf("segment:%d", index), text.Text)
				if next != text.Text {
					text.Text, changed = next, true
				}
			}
		}
		if changed {
			value.Refresh()
		}
	case *canvas.Text:
		text := l.text(object, "text", value.Text)
		if text != value.Text {
			value.Text = text
			value.Refresh()
		}
	case *widget.Menu:
		for _, item := range value.Items {
			visit(item)
		}
	}
	// Fyne's overlay/menu item types are internal, but expose their Content
	// and Item fields as public contracts. Read only those fields; never read
	// or mutate private renderer state or use unsafe.
	reflected := reflect.ValueOf(object)
	if reflected.Kind() != reflect.Pointer || reflected.IsNil() {
		return
	}
	reflected = reflected.Elem()
	if reflected.Kind() != reflect.Struct {
		return
	}
	if content := reflected.FieldByName("Content"); content.IsValid() && content.CanInterface() {
		if child, ok := content.Interface().(fyne.CanvasObject); ok {
			visit(child)
		}
	}
}

func (l *interfaceLocalization) selector(selectWidget *widget.Select) {
	callback := selectWidget.OnChanged
	if callback != nil {
		pointer := reflect.ValueOf(callback).Pointer()
		if l.selectors[selectWidget] != pointer {
			selectWidget.OnChanged = func(value string) { callback(uiSelectedValue(selectWidget, value)) }
			l.selectors[selectWidget] = reflect.ValueOf(selectWidget.OnChanged).Pointer()
		}
	}
	if translateChoices(selectWidget) {
		// Direct display updates deliberately do not invoke OnChanged: changing
		// the interface language never changes a recognition/model preference.
		selectWidget.Refresh()
	}
}

func localizeMenu(menu *fyne.Menu) {
	if menu == nil {
		return
	}
	menu.Label = uiText(menu.Label)
	for _, item := range menu.Items {
		item.Label = uiText(item.Label)
		localizeMenu(item.ChildMenu)
	}
}

func (d *desktop) changeInterfaceLanguage(language string) {
	if d.settings.InterfaceLocale() == (config.Settings{InterfaceLanguage: language}).InterfaceLocale() {
		return
	}
	updated := cloneSettings(d.settings)
	updated.InterfaceLanguage = language
	locale.Set(language)
	d.refreshAutomaticNames()
	d.refreshLocalizedHints()
	d.localizeInterface()
	d.persistPreferences(updated, func(err error) {
		if err != nil {
			locale.Set(d.settings.InterfaceLocale())
			d.refreshAutomaticNames()
			d.refreshLocalizedHints()
			d.localizeInterface()
			d.showError(uiText("Não foi possível salvar o idioma da interface: ")+err.Error(), "")
		}
	})
}

func (d *desktop) refreshLocalizedHints() {
	for _, doc := range d.documents {
		d.syncDocumentProvider(doc)
		d.syncPromptNavigation(doc)
		d.syncTranscriptionDetails(doc)
	}
	d.setAWSStatus(uiSource(d.awsLabel.Text), d.awsLabel.color)
}

func (d *desktop) refreshAutomaticNames() {
	for _, doc := range d.documents {
		if doc.name == "" {
			// renumberDocuments also synchronizes controls; do it once below.
			d.renumberDocuments()
			break
		}
	}
	if d.tabs != nil {
		d.tabs.Refresh()
	}
}
