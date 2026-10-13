package desktop

import (
	"fmt"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
)

func (d *desktop) groupDocuments(group uint64) []*document {
	var documents []*document
	for _, doc := range d.documents {
		if doc.group == group && !doc.closed {
			documents = append(documents, doc)
		}
	}
	return documents
}

func (d *desktop) groupName(group uint64) string {
	if name := d.groupNames[group]; name != "" {
		if d.groupAutomaticNames[group] == name {
			return uiText(name)
		}
		return name
	}
	return uiTextf("Grupo %d", group)
}

func (d *desktop) groupForItem(item *container.TabItem) uint64 {
	for _, doc := range d.documents {
		if doc.item == item {
			return doc.group
		}
	}
	return 0
}

func (d *desktop) groupMenu() *fyne.Menu {
	return d.groupMenuFor(d.document.group)
}

func (d *desktop) groupMenuFor(group uint64) *fyne.Menu {
	rename := fyne.NewMenuItem(uiText("Renomear grupo…"), d.guard(func() { d.openRenameGroup(group) }))
	rename.Disabled = d.tabsDialog || d.discardDialog
	create := fyne.NewMenuItem(uiText("Novo grupo"), d.guard(func() { d.addGroup() }))
	create.Icon = groupsIcon
	create.Disabled = len(d.documents) >= maxDocuments || d.tabsDialog || d.discardDialog
	closeGroup := fyne.NewMenuItem(uiText("Fechar grupo…"), d.guard(func() { d.requestCloseGroup(group) }))
	closeGroup.Icon = closeTabsIcon
	closeGroup.Disabled = len(d.groupDocuments(group)) == 0 || !d.canClearGroup(group) || d.tabsDialog || d.discardDialog
	clearGroup := fyne.NewMenuItem(uiText("Fechar abas deste grupo…"), d.guard(func() { d.requestCloseGroupTabs(group) }))
	clearGroup.Icon = closeTabsIcon
	clearGroup.Disabled = !d.canClearGroup(group) || d.tabsDialog || d.discardDialog
	clear := fyne.NewMenuItem(uiText("Fechar abas de todos os grupos…"), d.guard(d.requestCloseTabs))
	clear.Icon = closeTabsIcon
	clear.Disabled = d.capture != captureIdle || d.tabsDialog || d.discardDialog
	width := fyne.NewMenuItem(uiText("Largura do nome…"), d.guard(func() { d.openGroupNameWidth(group) }))
	width.Disabled = d.tabsDialog || d.discardDialog
	return fyne.NewMenu(uiText("Grupos e abas"),
		rename, closeGroup, clearGroup, fyne.NewMenuItemSeparator(), create, clear, width,
	)
}

func (d *desktop) groupTabsMenuFor(group uint64) *fyne.Menu {
	items := make([]*fyne.MenuItem, 0, len(d.groupDocuments(group))+7)
	for _, doc := range d.groupDocuments(group) {
		item := fyne.NewMenuItem(doc.item.Text, d.guard(func() { d.tabs.Select(doc.item) }))
		item.Checked = doc == d.document
		items = append(items, item)
	}
	items = append(items, fyne.NewMenuItemSeparator())
	items = append(items, d.groupMenuFor(group).Items...)
	return fyne.NewMenu(d.groupName(group), items...)
}

func (d *desktop) addGroup() *document {
	if d.closing || d.tabsDialog || d.discardDialog || len(d.documents) >= maxDocuments {
		d.status.SetText("Feche uma aba antes de abrir outro grupo.")
		return nil
	}
	previous, documents := d.document, append([]*document{}, d.documents...)
	groups := append([]uint64{}, d.groups...)
	name := d.nextGroupName()
	d.nextGroup++
	d.groups = append(d.groups, d.nextGroup)
	d.groupNames[d.nextGroup] = name
	d.groupAutomaticNames[d.nextGroup] = name
	doc := d.createDocumentInGroup(d.nextGroup)
	d.tabs.Append(doc.item)
	d.tabs.Select(doc.item)
	d.renumberDocuments()
	if err := d.saveWorkspace(); err != nil {
		doc.closed = true
		delete(d.groupNames, doc.group)
		delete(d.groupAutomaticNames, doc.group)
		d.groups, d.documents = groups, documents
		d.selectingTabs = true
		d.tabs.SetItems(documentItems(documents))
		d.tabs.Select(previous.item)
		d.selectingTabs = false
		d.selectDocument(previous)
		d.renumberDocuments()
		d.showError("Não foi possível salvar o novo grupo: "+err.Error(), "")
		return nil
	}
	d.content.Refresh()
	return doc
}

func (d *desktop) nextGroupName() string {
	occupied := make(map[string]bool, len(d.groups))
	for _, group := range d.groups {
		name := d.groupNames[group]
		if name == "" {
			name = fmt.Sprintf("Grupo %d", group)
		}
		occupied[name] = true
		occupied[d.groupName(group)] = true
	}
	for number := 1; ; number++ {
		name := fmt.Sprintf("Grupo %d", number)
		if !occupied[name] && !occupied[uiText(name)] {
			return name
		}
	}
}

func documentItems(documents []*document) []*container.TabItem {
	items := make([]*container.TabItem, 0, len(documents))
	for _, doc := range documents {
		items = append(items, doc.item)
	}
	return items
}

func (d *desktop) documentInsertion(group uint64) int {
	position := slices.Index(d.groups, group)
	for index, doc := range d.documents {
		if slices.Index(d.groups, doc.group) > position {
			return index
		}
	}
	return len(d.documents)
}

func (d *desktop) groupInsertion(group uint64, index int) int {
	documents := d.groupDocuments(group)
	if index < len(documents) {
		return slices.Index(d.documents, documents[index])
	}
	return d.documentInsertion(group)
}

func normalizeWorkspaceGroups(snapshot *workspaceSnapshot) bool {
	if len(snapshot.Groups) == 0 {
		for index := range snapshot.Tabs {
			tab := &snapshot.Tabs[index]
			if tab.Group == 0 {
				tab.Group = 1
			}
			if !slices.Contains(snapshot.Groups, tab.Group) {
				snapshot.Groups = append(snapshot.Groups, tab.Group)
			}
		}
	}
	seen := make(map[uint64]bool)
	for _, group := range snapshot.Groups {
		if group == 0 || seen[group] {
			return false
		}
		seen[group] = true
		snapshot.NextGroup = max(snapshot.NextGroup, group)
	}
	if len(seen) == 0 || len(seen) > maxDocuments {
		return false
	}
	if snapshot.GroupNames == nil {
		snapshot.GroupNames = make(map[uint64]string)
	}
	if snapshot.GroupAutomaticNames == nil {
		snapshot.GroupAutomaticNames = make(map[uint64]string)
	}
	for index, group := range snapshot.Groups {
		if _, exists := snapshot.GroupNames[group]; !exists {
			snapshot.GroupNames[group] = fmt.Sprintf("Grupo %d", index+1)
			snapshot.GroupAutomaticNames[group] = snapshot.GroupNames[group]
		}
	}
	for group, name := range snapshot.GroupNames {
		if !seen[group] || validateGroupName(name) != nil {
			return false
		}
	}
	for group, name := range snapshot.GroupAutomaticNames {
		if !seen[group] || name != snapshot.GroupNames[group] || !automaticGroupName(name) {
			return false
		}
	}
	populated := make(map[uint64]bool)
	for index := range snapshot.Tabs {
		tab := &snapshot.Tabs[index]
		if tab.Group == 0 {
			tab.Group = snapshot.Groups[0]
		}
		if !seen[tab.Group] {
			return false
		}
		populated[tab.Group] = true
	}
	return len(populated) == len(seen)
}

func (d *desktop) cancelMergeForGroup(group uint64, message string) {
	if d.mergeBusy && d.mergeGroup == group {
		d.cancelMerge(message)
	}
}
