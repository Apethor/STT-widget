package desktop

import (
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

type settingsPage struct {
	id, title, description string
	content                fyne.CanvasObject
}

type settingsSearchEntry struct {
	title, description, page string
	target                   fyne.CanvasObject
}

type settingsSections struct {
	Items         []*container.TabItem
	tree          *widget.Tree
	pageStack     *fyne.Container
	pageViewport  *container.Scroll
	search        *widget.Entry
	searchResults *fyne.Container
	searchList    *widget.List
	pages         map[string]fyne.CanvasObject
	scrolls       map[string]*container.Scroll
	idsByTitle    map[string]string
	selectedID    string
	searchItems   []settingsSearchEntry
	searchResult  []int
	treeSelected  string
}

func newSettingsSections(canvas fyne.Canvas, pages []settingsPage, compatibility []*container.TabItem, compatibilityIDs []string) *settingsSections {
	sections := &settingsSections{
		Items: compatibility, pages: make(map[string]fyne.CanvasObject),
		idsByTitle: make(map[string]string), scrolls: make(map[string]*container.Scroll),
	}
	for _, page := range pages {
		heading := widget.NewRichTextWithText(page.title)
		heading.Segments[0].(*widget.TextSegment).Style = widget.RichTextStyleHeading
		description := widget.NewLabel(page.description)
		description.Wrapping = fyne.TextWrapWord
		header := container.NewVBox(heading, description, widget.NewSeparator())
		scroll := container.NewVScroll(page.content)
		body := fyne.CanvasObject(scroll)
		if page.id == "audio" || page.id == "instructions" || strings.HasPrefix(page.id, "instructions/") {
			body = page.content
		} else {
			sections.scrolls[page.id] = scroll
		}
		sections.pages[page.id] = container.NewBorder(
			header, nil, nil, nil, body,
		)
		sections.searchItems = append(sections.searchItems,
			settingsSearchEntry{title: page.title, description: page.description, page: page.id})
	}
	for index, item := range compatibility {
		if index < len(compatibilityIDs) {
			sections.idsByTitle[item.Text] = compatibilityIDs[index]
		}
	}

	children := map[string][]string{
		"":             {"connections", "audio", "visual", "video", "llm", "instructions", "data", "about"},
		"connections":  {"connections/aws", "connections/api"},
		"audio":        {"audio/conversation", "audio/capture", "audio/aws", "audio/api", "audio/whisper"},
		"instructions": {"instructions/teams", "instructions/merge", "instructions/custom"},
		"data":         {"data/cleanup"},
	}
	labels := map[string]string{
		"connections": "Conexões", "connections/aws": "AWS", "connections/api": "API",
		"audio": "Áudio", "audio/capture": "Microfone e captura", "audio/aws": "AWS Cloud",
		"audio/conversation": "Conversa ao vivo",
		"audio/api":          "API", "audio/whisper": "Whisper",
		"visual": "Visual", "llm": "LLM", "instructions": "Instruções",
		"video":              "Vídeo",
		"instructions/teams": "Teams", "instructions/merge": "Mesclar",
		"instructions/custom": "Personalizadas",
		"data":                "Dados", "data/cleanup": "Limpeza", "about": "Sobre",
	}
	sections.tree = widget.NewTree(
		func(uid string) []string { return children[uid] },
		func(uid string) bool { return len(children[uid]) > 0 },
		func(bool) fyne.CanvasObject { return widget.NewLabel("") },
		func(uid string, branch bool, object fyne.CanvasObject) {
			row := object.(*widget.Label)
			row.TextStyle.Bold = branch || uid == sections.selectedID
			row.SetText(uiText(labels[uid]))
		},
	)
	sections.tree.HideSeparators = true
	sections.tree.OpenBranch("connections")
	sections.tree.OpenBranch("audio")
	sections.tree.OpenBranch("instructions")
	sections.tree.OnSelected = sections.selectID
	sections.pageStack = container.NewMax()
	sections.search = widget.NewEntry()
	sections.search.SetPlaceHolder(uiText("Buscar configurações"))
	sections.searchList = widget.NewList(
		func() int { return len(sections.searchResult) },
		func() fyne.CanvasObject {
			title := widget.NewLabel("")
			title.TextStyle.Bold = true
			description := widget.NewLabel("")
			description.Wrapping = fyne.TextWrapWord
			return container.NewVBox(title, description)
		},
		func(index widget.ListItemID, object fyne.CanvasObject) {
			entry := sections.searchItems[sections.searchResult[index]]
			row := object.(*fyne.Container)
			row.Objects[0].(*widget.Label).SetText(uiText(entry.title))
			row.Objects[1].(*widget.Label).SetText(uiText(entry.description))
		},
	)
	sections.searchList.OnSelected = func(index widget.ListItemID) {
		if index < 0 || index >= len(sections.searchResult) {
			return
		}
		entry := sections.searchItems[sections.searchResult[index]]
		sections.selectID(entry.page)
		sections.reveal(entry)
		sections.searchResults.Hide()
		if focusable, ok := entry.target.(fyne.Focusable); ok {
			canvas.Focus(focusable)
		} else {
			canvas.Focus(sections.search)
		}
	}
	// Results share the settings dialog's focus manager with the entry.
	// A separate popup takes the native keyboard focus after the first rune.
	sections.searchResults = container.NewGridWrap(fyne.NewSize(440, 64), sections.searchList)
	sections.searchResults.Hide()
	sections.search.OnChanged = func(query string) {
		sections.searchResult = sections.searchResult[:0]
		query = strings.ToLower(strings.TrimSpace(query))
		if query == "" {
			sections.searchResults.Hide()
			return
		}
		for index, entry := range sections.searchItems {
			if strings.Contains(strings.ToLower(uiText(entry.title)+" "+uiText(entry.description)), query) ||
				strings.Contains(strings.ToLower(entry.title+" "+entry.description), query) {
				sections.searchResult = append(sections.searchResult, index)
			}
		}
		sections.searchList.Refresh()
		if len(sections.searchResult) == 0 {
			sections.searchResults.Hide()
			return
		}
		height := min(float32(300), float32(len(sections.searchResult))*56+8)
		width := max(float32(160), min(float32(440), canvas.Size().Width-32))
		sections.searchResults.Layout = layout.NewGridWrapLayout(fyne.NewSize(width, height))
		sections.searchResults.Show()
		sections.searchResults.Refresh()
	}
	sections.selectID("audio")
	return sections
}

func (s *settingsSections) Object() fyne.CanvasObject {
	searchRow := container.NewVBox(s.search, s.searchResults)
	navigation := container.New(insetLayout{minimum: fyne.NewSize(200, 0)}, s.tree)
	s.pageViewport = container.NewHScroll(s.pageStack)
	workspace := container.NewHSplit(navigation, s.pageViewport)
	workspace.Offset = 0.25
	return container.NewBorder(searchRow, nil, nil, nil, workspace)
}

func (s *settingsSections) SelectIndex(index int) {
	if index < 0 || index >= len(s.Items) {
		return
	}
	s.Select(s.Items[index])
}

func (s *settingsSections) Select(item *container.TabItem) {
	if item == nil {
		return
	}
	for _, candidate := range s.Items {
		if candidate == item {
			s.selectID(s.idsByTitle[item.Text])
			return
		}
	}
}

func (s *settingsSections) Selected() *container.TabItem {
	for _, item := range s.Items {
		if s.idsByTitle[item.Text] == s.selectedID {
			return item
		}
	}
	return nil
}

func (s *settingsSections) selectID(id string) {
	page, exists := s.pages[id]
	if !exists {
		return
	}
	s.selectedID = id
	s.pageStack.Objects = []fyne.CanvasObject{page}
	s.pageStack.Refresh()
	if s.pageViewport != nil {
		s.pageViewport.ScrollToOffset(fyne.NewPos(0, 0))
	}
	s.tree.Refresh()
	if s.tree != nil && s.treeSelected != id {
		s.treeSelected = id
		s.tree.Select(id)
	}
}

func (s *settingsSections) link(text, page string) fyne.CanvasObject {
	link := widget.NewHyperlink(text, nil)
	link.OnTapped = func() { s.selectID(page) }
	return link
}

func (s *settingsSections) reveal(entry settingsSearchEntry) {
	if entry.target == nil {
		return
	}
	driver := fyne.CurrentApp().Driver()
	if viewport := s.pageViewport; viewport != nil {
		left := driver.AbsolutePositionForObject(entry.target).X -
			driver.AbsolutePositionForObject(s.pageStack).X
		offset := viewport.Offset
		if left < offset.X || entry.target.Size().Width >= viewport.Size().Width {
			offset.X = left
		} else if right := left + entry.target.Size().Width; right > offset.X+viewport.Size().Width {
			offset.X = right - viewport.Size().Width
		}
		viewport.ScrollToOffset(offset)
	}
	scroll := s.scrolls[entry.page]
	if scroll == nil {
		return
	}
	top := driver.AbsolutePositionForObject(entry.target).Y -
		driver.AbsolutePositionForObject(scroll.Content).Y
	offset := scroll.Offset
	if top < offset.Y {
		offset.Y = top
	} else if bottom := top + entry.target.Size().Height; bottom > offset.Y+scroll.Size().Height {
		offset.Y = bottom - scroll.Size().Height
	}
	scroll.ScrollToOffset(offset)
}

func (s *settingsSections) addSearchEntries(entries ...settingsSearchEntry) {
	s.searchItems = append(s.searchItems, entries...)
	slices.SortFunc(s.searchItems, func(a, b settingsSearchEntry) int {
		return strings.Compare(strings.ToLower(a.title), strings.ToLower(b.title))
	})
}
