package desktop

import (
	"math"

	"ai-talker/internal/config"

	"fyne.io/fyne/v2"
)

// EditorOffset always measures the transcription share, regardless of order.
type panelSizes struct {
	EditorOffset      float64 `json:"editor_offset,omitempty"`
	RecorderOffset    float64 `json:"recorder_offset,omitempty"`
	ColumnHeight      float32 `json:"column_height,omitempty"`
	RecorderHeight    float32 `json:"recorder_height,omitempty"`
	RecorderMode      string  `json:"recorder_mode,omitempty"`
	RecorderCollapsed bool    `json:"recorder_collapsed,omitempty"`
}

func (s panelSizes) valid() bool {
	return s.EditorOffset >= 0 && s.EditorOffset <= 1 &&
		s.RecorderOffset >= 0 && s.RecorderOffset <= 1 &&
		s.ColumnHeight >= 0 && s.ColumnHeight <= 100000 &&
		!math.IsNaN(float64(s.ColumnHeight)) &&
		s.RecorderHeight >= 0 && s.RecorderHeight <= 100000 &&
		!math.IsNaN(float64(s.RecorderHeight)) &&
		(s.RecorderMode == "" || s.RecorderMode == config.RecordingSmall ||
			s.RecorderMode == config.RecordingMedium || s.RecorderMode == config.RecordingLarge)
}

func (s panelSizes) normalized(legacy float64) panelSizes {
	if s.EditorOffset <= 0 || s.EditorOffset >= 1 {
		s.EditorOffset = 0.5
		if legacy > 0 && legacy < 1 {
			s.EditorOffset = legacy
		}
	}
	return s
}

func (d *desktop) rememberPanelSizes(doc *document) {
	d.rememberPanelSizesWithOrder(doc, d.settings.Appearance.InvertPanels)
}

func (d *desktop) rememberPanelSizesWithOrder(doc *document, inverted bool) {
	if doc == nil || doc.editors == nil || doc.closed {
		return
	}
	before := doc.panelSizes
	if !doc.processCollapsed && !doc.transcriptCollapsed {
		offset := doc.editors.Offset
		if inverted {
			offset = 1 - offset
		}
		if offset > 0 && offset < 1 {
			doc.panelSizes.EditorOffset = offset
		}
	}
	if doc == d.document {
		if d.rowSplit != nil && !doc.panelSizes.RecorderCollapsed && !(doc.processCollapsed && doc.transcriptCollapsed) {
			doc.panelSizes.RecorderOffset = d.rowSplit.Offset
		}
		doc.panelSizes.ColumnHeight = d.columnHeight
	}
	if before.EditorOffset != 0 && before.EditorOffset != doc.panelSizes.EditorOffset ||
		before.RecorderOffset != 0 && before.RecorderOffset != doc.panelSizes.RecorderOffset ||
		before.ColumnHeight != doc.panelSizes.ColumnHeight {
		doc.presentationEpoch++
	}
}

func (d *desktop) restoreOuterPanelSizes(doc *document) {
	if doc == nil {
		return
	}
	if d.recorderLayout != nil {
		d.recorderLayout.mode = d.recordingMode(doc)
		d.syncRecordingSizeButton()
		if d.recorderLayout.surface != nil {
			d.recorderLayout.surface.Refresh()
		}
		if d.recordPanel != nil {
			d.recordPanel.Refresh()
		}
	}
	d.columnHeight = doc.panelSizes.ColumnHeight
	if d.columnScroll != nil {
		if body, ok := d.columnScroll.Content.(*fyne.Container); ok {
			if layout, ok := body.Layout.(*columnLayout); ok {
				layout.height = d.columnHeight
				body.Refresh()
				d.columnScroll.Refresh()
			}
		}
	}
	if d.rowSplit != nil {
		offset := doc.panelSizes.RecorderOffset
		if doc.processCollapsed && doc.transcriptCollapsed && !doc.panelSizes.RecorderCollapsed {
			offset = 1
		}
		if doc.panelSizes.RecorderCollapsed && !d.rowSplit.Horizontal {
			available := d.rowSplit.Size().Height
			if d.recorderDivider != nil {
				available -= d.recorderDivider.dividerHeight()
			}
			if available > 0 {
				offset = float64(d.recordPanel.MinSize().Height / available)
			}
		}
		if offset > 0 && offset <= 1 {
			d.rowSplit.SetOffset(offset)
		}
	}
	// The stacked VBox and the inline recording column own their child's
	// height. Refresh the parent tree after changing the recorder surface so
	// they recalculate that height immediately, including on tab selection.
	if d.window.Content() != nil {
		d.window.Content().Refresh()
	}
}

func (d *desktop) recordingMode(doc *document) string {
	if doc != nil && doc.panelSizes.RecorderMode != "" {
		return doc.panelSizes.RecorderMode
	}
	return d.settings.Appearance.RecordingSize
}

func (d *desktop) arrangeEditorPanels(doc *document) {
	if doc.transcriptPanel == nil || doc.processPanel == nil {
		return
	}
	doc.editors.Leading, doc.editors.Trailing = doc.transcriptPanel, doc.processPanel
	conversation := doc.voice != nil && doc.voice.Mode == voiceModeLive
	if conversation {
		d.ensureResponsePanel(doc)
		doc.editors.Trailing = doc.voice.panel
	}
	doc.panelSizes = doc.panelSizes.normalized(doc.processOffset)
	offset := doc.panelSizes.EditorOffset
	if doc.processCollapsed && !doc.transcriptCollapsed && !conversation {
		offset = 1
	} else if doc.transcriptCollapsed && (!doc.processCollapsed || conversation) {
		offset = 0
	}
	if d.settings.Appearance.InvertPanels {
		doc.editors.Leading, doc.editors.Trailing = doc.editors.Trailing, doc.editors.Leading
		offset = 1 - offset
	}
	doc.transcriptPanel.Refresh()
	doc.processPanel.Refresh()
	doc.editors.SetOffset(offset)
}
