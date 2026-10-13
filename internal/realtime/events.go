package realtime

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

type wireEvent struct {
	CompletionID             string `json:"completionId"`
	ContentID                string `json:"contentId"`
	Type                     string `json:"type"`
	Role                     string `json:"role"`
	AdditionalModelFields    string `json:"additionalModelFields"`
	Content                  string `json:"content"`
	StopReason               string `json:"stopReason"`
	AudioOutputConfiguration *struct {
		MediaType       string `json:"mediaType"`
		SampleRateHertz int    `json:"sampleRateHertz"`
		SampleSizeBits  int    `json:"sampleSizeBits"`
		ChannelCount    int    `json:"channelCount"`
	} `json:"audioOutputConfiguration"`
}

func (s *Session) accept(name string, body json.RawMessage, blocks map[string]*content, order *[]string) error {
	var item wireEvent
	if json.Unmarshal(body, &item) != nil {
		return errors.New("o serviço enviou dados Nova Sonic inválidos")
	}
	switch name {
	case "completionStart":
		if item.CompletionID == "" {
			return errors.New("a resposta Nova Sonic não informou seu identificador")
		}
		s.logicalTurn(item.CompletionID, "", "", "", false)
	case "contentStart":
		if item.Type != "TEXT" && item.Type != "AUDIO" {
			return nil
		}
		if item.ContentID == "" || item.CompletionID == "" || blocks[item.ContentID] != nil {
			return errors.New("o conteúdo Nova Sonic não informou identificadores únicos")
		}
		var extra struct {
			GenerationStage string `json:"generationStage"`
		}
		if item.AdditionalModelFields != "" && json.Unmarshal([]byte(item.AdditionalModelFields), &extra) != nil {
			return errors.New("o estágio do texto Nova Sonic é inválido")
		}
		logicalID := s.logicalTurn(item.CompletionID, item.Role, item.Type, extra.GenerationStage, true)
		block := &content{
			id: item.ContentID, completion: logicalID, role: item.Role,
			kind: item.Type, stage: extra.GenerationStage,
		}
		blocks[item.ContentID] = block
		*order = append(*order, item.ContentID)
		s.mu.Lock()
		i := s.responseIndex(logicalID)
		if item.Role == "ASSISTANT" {
			s.current = logicalID
		}
		if item.Type == "AUDIO" && item.Role == "ASSISTANT" && item.AudioOutputConfiguration != nil {
			format := item.AudioOutputConfiguration
			if format.MediaType != "audio/lpcm" || format.SampleSizeBits != 16 || format.ChannelCount != 1 ||
				!slices.Contains([]int{8000, 16000, 24000}, format.SampleRateHertz) {
				s.mu.Unlock()
				return errors.New("o áudio de resposta Nova Sonic não é PCM mono de 16 bits")
			}
			if len(s.responses[i].PCM) != 0 && s.responses[i].SampleRate != format.SampleRateHertz {
				s.mu.Unlock()
				return errors.New("o serviço trocou a taxa de áudio no meio da resposta")
			}
			s.responses[i].SampleRate = format.SampleRateHertz
		}
		s.mu.Unlock()
	case "textOutput":
		block := blocks[item.ContentID]
		if block == nil || block.closed || block.kind != "TEXT" {
			return nil
		}
		var interruption struct {
			Interrupted bool `json:"interrupted"`
		}
		if json.Unmarshal([]byte(item.Content), &interruption) == nil && interruption.Interrupted {
			s.interruptResponse(block.completion)
			return nil
		}
		s.mu.Lock()
		suppressed := s.suppressed[block.completion] && block.role == "ASSISTANT"
		s.mu.Unlock()
		if suppressed || block.role == "USER" && block.stage != "FINAL" {
			return nil
		}
		if block.text.Len()+len(item.Content) > 240000 {
			return errors.New("o texto da resposta Nova Sonic excedeu o limite")
		}
		block.text.WriteString(item.Content)
		s.updateText(block, blocks, *order, false)
	case "audioOutput":
		block := blocks[item.ContentID]
		if block == nil || block.closed || block.kind != "AUDIO" || block.role != "ASSISTANT" {
			return nil
		}
		pcm, err := base64.StdEncoding.DecodeString(item.Content)
		if err != nil || len(pcm)%2 != 0 {
			return errors.New("o serviço enviou áudio PCM inválido")
		}
		if len(pcm) == 0 {
			return nil
		}
		s.mu.Lock()
		if s.suppressed[block.completion] {
			s.mu.Unlock()
			return nil
		}
		i := s.responseIndex(block.completion)
		if len(s.responses[i].PCM)+len(pcm) > maxResponseBytes {
			s.mu.Unlock()
			return errors.New("o áudio de resposta Nova Sonic excedeu o limite; o conteúdo parcial foi preservado")
		}
		s.responses[i].PCM = append(s.responses[i].PCM, pcm...)
		rate := s.responses[i].SampleRate
		s.mu.Unlock()
		s.emit(Event{Kind: AssistantAudio, CompletionID: block.completion, ContentID: block.id, PCM: pcm, SampleRate: rate})
	case "contentEnd":
		block := blocks[item.ContentID]
		if block == nil || block.closed {
			return nil
		}
		block.closed = true
		if item.StopReason == "INTERRUPTED" {
			s.interruptResponse(block.completion)
		}
		if block.kind == "TEXT" && (block.role == "ASSISTANT" || block.stage == "FINAL") {
			s.updateText(block, blocks, *order, true)
		}
		// Nova 2.5 keeps its completion ID across multiple live turns.
		// The AUDIO END_TURN boundary completes one spoken response.
		if block.kind == "AUDIO" && block.role == "ASSISTANT" && item.StopReason == "END_TURN" {
			s.finishResponse(block.completion, item.StopReason)
		}
	case "completionEnd":
		if item.CompletionID == "" {
			return errors.New("o término da resposta Nova Sonic não informou seu identificador")
		}
		logicalID := s.wireActive[item.CompletionID]
		if logicalID == "" {
			logicalID = s.logicalTurn(item.CompletionID, "", "", "", false)
		}
		s.finishResponse(logicalID, item.StopReason)
	}
	return nil
}

// Assign an application turn ID shared by USER transcription and ASSISTANT
// response. Late FINAL assistant text still belongs to its preceding audio.
func (s *Session) logicalTurn(wireID, role, kind, stage string, contentStart bool) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.wireActive[wireID]
	newTurn := id == ""
	if id != "" && contentStart {
		i := s.responseIndex(id)
		response := s.responses[i]
		hasReply := response.Text != "" || len(response.PCM) != 0 || response.Complete || response.Interrupted
		switch {
		case role == "USER" && kind == "TEXT" && stage == "FINAL":
			newTurn = hasReply && (response.UserText != "" || response.Complete || response.Interrupted)
		case role == "ASSISTANT" && kind == "TEXT" && stage == "SPECULATIVE":
			newTurn = response.Complete
		case role == "ASSISTANT" && kind == "AUDIO":
			newTurn = response.Complete
		}
	}
	if newTurn {
		s.wireSequence[wireID]++
		id = wireID
		if s.wireSequence[wireID] > 1 {
			id = fmt.Sprintf("%s/%d", wireID, s.wireSequence[wireID])
		}
		s.wireActive[wireID] = id
		s.responseIndex(id)
	}
	s.current = id
	return id
}

func (s *Session) finishResponse(id, reason string) {
	if reason == "INTERRUPTED" {
		s.interruptResponse(id)
	}
	s.mu.Lock()
	i := s.responseIndex(id)
	alreadyComplete := s.responses[i].Complete
	s.responses[i].Complete = !s.responses[i].Interrupted && reason == "END_TURN"
	response := s.responses[i]
	s.mu.Unlock()
	if alreadyComplete && response.Complete {
		return
	}
	s.emit(Event{
		Kind: ResponseEnd, CompletionID: id, Text: response.Text,
		SampleRate: response.SampleRate, Final: response.Complete, Interrupted: response.Interrupted,
	})
}

func (s *Session) updateText(block *content, blocks map[string]*content, order []string, final bool) {
	if block.role != "USER" && block.role != "ASSISTANT" {
		return
	}
	// Speculative response text is replaced by FINAL spoken text, rather than
	// appended a second time. USER text is accepted only at the FINAL stage.
	hasFinal := false
	for _, id := range order {
		candidate := blocks[id]
		hasFinal = hasFinal || candidate.completion == block.completion && candidate.role == block.role &&
			candidate.stage == "FINAL" && candidate.text.Len() != 0
	}
	var parts []string
	for _, id := range order {
		candidate := blocks[id]
		if candidate.completion != block.completion || candidate.role != block.role || candidate.kind != "TEXT" {
			continue
		}
		if hasFinal && candidate.stage != "FINAL" || block.role == "USER" && candidate.stage != "FINAL" {
			continue
		}
		if text := strings.TrimSpace(candidate.text.String()); text != "" {
			parts = append(parts, text)
		}
	}
	text := strings.Join(parts, " ")
	s.mu.Lock()
	if block.role == "ASSISTANT" && s.suppressed[block.completion] {
		s.mu.Unlock()
		return
	}
	i := s.responseIndex(block.completion)
	kind := AssistantText
	if block.role == "USER" {
		s.responses[i].UserText = text
		kind = UserText
	} else {
		s.responses[i].Text = text
	}
	s.mu.Unlock()
	s.emit(Event{
		Kind: kind, CompletionID: block.completion, ContentID: block.id,
		Text: text, Final: final,
	})
}
