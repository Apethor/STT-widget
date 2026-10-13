package desktop

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ai-talker/internal/apiconfig"
	"ai-talker/internal/config"
	"ai-talker/internal/processing"
	"ai-talker/internal/secrets"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

type testVault struct {
	mu           sync.Mutex
	values       map[string]string
	writeErr     error
	deleteErrors map[string]error
}

func (v *testVault) Read(target string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	key, ok := v.values[target]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return key, nil
}
func (v *testVault) Write(target, value string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.writeErr != nil {
		return v.writeErr
	}
	if v.values == nil {
		v.values = make(map[string]string)
	}
	v.values[target] = value
	return nil
}
func (v *testVault) Delete(target string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.deleteErrors[target]; err != nil {
		return err
	}
	delete(v.values, target)
	return nil
}

func TestConnectionManagerIncludesAllFlowsAndLeavesActiveSelectionsIndependent(t *testing.T) {
	h := newHarness(t, nil, nil)
	audio := apiconfig.SavedConnection{
		ID: strings.Repeat("a", 32), Name: "Minha voz", Provider: apiconfig.Generic,
		Endpoint: "https://audio.example/v1", Audio: true,
	}
	text := apiconfig.SavedConnection{
		ID: strings.Repeat("b", 32), Name: "Minha escrita", Provider: apiconfig.Azure,
		Endpoint: "https://text.openai.azure.com/openai/v1", Text: true,
	}
	h.d.settings.APIConnections = []apiconfig.SavedConnection{audio, text}
	h.d.settings.AudioAPI = apiconfig.Audio{Connection: audio.Connection(), Model: "audio-model"}
	options := h.d.document.processOptions
	options.Provider, options.API, options.ModelID = "api", text.Connection(), "deployment-model"
	h.d.changeProcessingOptions(h.d.document, options)
	h.d.openSettings()
	main := h.d.settingsForm
	h.await(func() bool { return !main.loading })
	manager := main.audioAPI.manager
	if manager == nil || !manager.managerAll ||
		manager.choices[apiConnectionLabel(audio)] != audio.ID ||
		manager.choices[apiConnectionLabel(text)] != text.ID {
		t.Fatal("a página de API não montou o catálogo completo de Texto e Áudio")
	}
	if main.audioAPI.choices[apiConnectionLabel(text)] != "" ||
		main.llm.api.choices[apiConnectionLabel(audio)] != "" {
		t.Fatal("os seletores ativos mostraram conexões incompatíveis com seus fluxos")
	}
	manager.managerSelect.SetSelected(apiConnectionLabel(text))
	if manager.connection().Provider != apiconfig.Azure || manager.audioEnabled.Checked ||
		!manager.textEnabled.Checked || !manager.audioEnabled.Disabled() ||
		main.audioAPI.connectionID != audio.ID || main.llm.api.connectionID != text.ID {
		t.Fatal("gerenciar uma conexão trocou a seleção ativa ou permitiu Azure de texto para áudio")
	}
	h.d.newAPIConnection(manager)
	if !manager.textEnabled.Checked || !manager.audioEnabled.Checked {
		t.Fatal("uma conexão compatível nova não ofereceu os dois fluxos")
	}
}

func TestEditingCatalogEndpointUpdatesActiveCopiesAndKeepsFlowModels(t *testing.T) {
	h := newHarness(t, nil, nil)
	connection := apiconfig.SavedConnection{
		ID: strings.Repeat("c", 32), Name: "Compartilhada", Provider: apiconfig.Generic,
		Endpoint: "https://old.example/v1", Text: true, Audio: true,
	}
	h.d.settings.APIConnections = []apiconfig.SavedConnection{connection}
	h.d.settings.AudioAPI = apiconfig.Audio{Connection: connection.Connection(), Model: "voice-model", Language: "pt"}
	options := h.d.document.processOptions
	options.Provider, options.API, options.ModelID = "api", connection.Connection(), "writing-model"
	h.d.settings.Processing = options
	h.d.changeProcessingOptions(h.d.document, options)
	h.d.openSettings()
	main := h.d.settingsForm
	h.await(func() bool { return !main.loading })
	manager := main.audioAPI.manager
	manager.endpoint.SetText("https://new.example/v1")
	test.Tap(manager.saveConnection)
	h.await(func() bool { return !manager.busy })
	if h.d.settings.AudioAPI.Endpoint != "https://new.example/v1" ||
		h.d.settings.Processing.API.Endpoint != "https://new.example/v1" ||
		h.d.document.processOptions.API.Endpoint != "https://new.example/v1" ||
		main.audioAPI.connection().Endpoint != "https://new.example/v1" ||
		main.llm.api.connection().Endpoint != "https://new.example/v1" {
		t.Fatal("editar o endpoint deixou uma seleção ativa com o endereço anterior")
	}
	if h.d.settings.AudioAPI.Model != "voice-model" || h.d.settings.AudioAPI.Language != "pt" ||
		h.d.document.processOptions.ModelID != "writing-model" {
		t.Fatal("editar a conexão alterou os modelos/idioma dos fluxos")
	}
}

func TestCatalogRemovalRestoresSecretWhenSavingPreferencesFails(t *testing.T) {
	connection := apiconfig.SavedConnection{
		ID: strings.Repeat("d", 32), Name: "Sem uso", Provider: apiconfig.Generic,
		Endpoint: "https://unused.example/v1", Text: true,
	}
	vault := &testVault{values: map[string]string{connection.Connection().CredentialTarget(): "synthetic-key"}}
	h := newHarness(t, nil, func(deps *dependencies) { deps.vault = vault })
	h.d.settings.APIConnections = []apiconfig.SavedConnection{connection}
	h.d.openSettings()
	main := h.d.settingsForm
	h.await(func() bool { return !main.loading })
	manager := main.audioAPI.manager
	manager.managerSelect.SetSelected(apiConnectionLabel(connection))
	if err := os.Mkdir(filepath.Join(h.d.directory, "settings.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	test.Tap(manager.removeConnection)
	h.await(func() bool { return !manager.busy })
	if _, exists := h.d.apiSavedConnection(connection.ID); !exists {
		t.Fatal("a falha de persistência removeu a conexão da memória")
	}
	key, err := vault.Read(connection.Connection().CredentialTarget())
	if err != nil || key != "synthetic-key" {
		t.Fatal("a remoção não restaurou a chave quando as preferências falharam")
	}
	if strings.Contains(manager.status.Text, "synthetic-key") ||
		!strings.Contains(manager.status.Text, "preservadas") {
		t.Fatal("a falha expôs a chave ou foi apresentada como sucesso")
	}
}

func TestDeletingMigratedKeyRemovesLegacyTargetAndRollsBackPartialFailure(t *testing.T) {
	connection := apiconfig.Connection{Provider: apiconfig.Generic, Endpoint: "https://legacy.example/v1"}
	connection.ID = apiconfig.LegacyConnectionID(connection)
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure=%t", fail), func(t *testing.T) {
			vault := &testVault{values: map[string]string{
				connection.CredentialTarget():       "new-synthetic-key",
				connection.LegacyCredentialTarget(): "old-synthetic-key",
			}}
			if fail {
				vault.deleteErrors = map[string]error{connection.LegacyCredentialTarget(): errors.New("vault unavailable")}
			}
			h := newHarness(t, nil, func(deps *dependencies) { deps.vault = vault })
			restore, err := h.d.deleteAPIConnectionSecrets(connection, h.d.settings.APIConnections)
			if fail {
				if err == nil {
					t.Fatal("a falha parcial foi apresentada como remoção concluída")
				}
				key, readErr := vault.Read(connection.CredentialTarget())
				if readErr != nil || key != "new-synthetic-key" {
					t.Fatal("a falha no legado perdeu a chave da conexão nomeada")
				}
				return
			}
			if err != nil || restore == nil {
				t.Fatal("a remoção de credenciais falhou", err)
			}
			for _, target := range []string{connection.CredentialTarget(), connection.LegacyCredentialTarget()} {
				if _, err := vault.Read(target); !errors.Is(err, secrets.ErrNotFound) {
					t.Fatal("uma chave legada permaneceu disponível para ressuscitar a credencial removida")
				}
			}
		})
	}
}

func TestNamedAccountKeyRemovalDoesNotTouchAnotherLegacyAccount(t *testing.T) {
	connection := apiconfig.Connection{
		ID: strings.Repeat("e", 32), Provider: apiconfig.Generic, Endpoint: "https://shared.example/v1",
	}
	vault := &testVault{values: map[string]string{
		connection.CredentialTarget():       "named-synthetic-key",
		connection.LegacyCredentialTarget(): "legacy-synthetic-key",
	}}
	h := newHarness(t, nil, func(deps *dependencies) { deps.vault = vault })
	if _, err := h.d.deleteAPIConnectionSecrets(connection, h.d.settings.APIConnections); err != nil {
		t.Fatal(err)
	}
	key, err := vault.Read(connection.LegacyCredentialTarget())
	if err != nil || key != "legacy-synthetic-key" {
		t.Fatal("remover uma conta apagou a credencial de outra conta no mesmo endpoint")
	}
}

func TestLegacyKeyRemovalPreservesAnotherPresetThatHasNotMigratedYet(t *testing.T) {
	other := apiconfig.Connection{Provider: apiconfig.OpenAI}
	other.ID = apiconfig.LegacyConnectionID(other)
	current := apiconfig.Connection{Provider: apiconfig.Generic, Endpoint: other.BaseURL()}
	current.ID = apiconfig.LegacyConnectionID(current)
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("migration-write-fails=%t", fail), func(t *testing.T) {
			vault := &testVault{values: map[string]string{
				current.CredentialTarget():       "current-synthetic-key",
				current.LegacyCredentialTarget(): "shared-synthetic-key",
			}}
			if fail {
				vault.writeErr = errors.New("vault unavailable")
			}
			h := newHarness(t, nil, func(deps *dependencies) { deps.vault = vault })
			h.d.settings.APIConnections = []apiconfig.SavedConnection{
				{ID: current.ID, Name: "Áudio legado", Provider: current.Provider, Endpoint: current.Endpoint, Audio: true},
				{ID: other.ID, Name: "Texto legado", Provider: other.Provider, Text: true},
			}
			_, err := h.d.deleteAPIConnectionSecrets(current, h.d.settings.APIConnections)
			if fail {
				if err == nil {
					t.Fatal("a falha de preservar outra conexão foi ignorada")
				}
				for _, target := range []string{current.CredentialTarget(), current.LegacyCredentialTarget()} {
					if _, err := vault.Read(target); err != nil {
						t.Fatal("a tentativa falha de migração apagou uma credencial necessária")
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if key, err := vault.Read(other.CredentialTarget()); err != nil || key != "shared-synthetic-key" {
				t.Fatal("remover a chave de um preset perdeu a chave segura de outro preset legado")
			}
			if _, err := vault.Read(current.LegacyCredentialTarget()); !errors.Is(err, secrets.ErrNotFound) {
				t.Fatal("a chave removida ainda poderia ser reutilizada pelo preset excluído")
			}
		})
	}
}

func TestDefaultConnectionNameRemainsUniqueAfterDeletion(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.d.settings.APIConnections = []apiconfig.SavedConnection{{
		ID: strings.Repeat("f", 32), Name: "Áudio 2", Provider: apiconfig.OpenAI, Audio: true,
	}}
	if name := h.d.defaultAPIConnectionName(true); name == "Áudio 2" {
		t.Fatal("o nome sugerido colidiu com uma conexão preservada após excluir outra")
	}
}

func TestAudioAPIKeyIsSeparateFromPreferencesAndIsNotReusedForAnotherEndpoint(t *testing.T) {
	vault := new(testVault)
	var verifications atomic.Int32
	h := newHarness(t, nil, func(deps *dependencies) {
		deps.vault = vault
		deps.verify = func(_ context.Context, _ config.Settings) error { verifications.Add(1); return nil }
	})
	h.d.openAudioAPISettings()
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	f := form.audioAPI.manager
	f.provider.SetSelected(genericAPIChoice)
	f.endpoint.SetText("https://audio.example/v1")
	form.audioAPI.model.SetText("manual-audio-model")
	test.Tap(f.saveConnection)
	h.await(func() bool { return !f.busy })
	saved, exists := h.d.apiSavedConnection(f.connectionID)
	if !exists {
		t.Fatal("o gestor visível não salvou a conexão")
	}
	form.audioAPI.activeSelect.SetSelected(apiConnectionLabel(saved))
	f.key.SetText("fictitious-desktop-secret")
	test.Tap(f.saveKey)
	h.await(func() bool { return !f.busy })
	connection := f.connection()
	key, err := vault.Read(connection.CredentialTarget())
	if err != nil || key != "fictitious-desktop-secret" || f.key.Text != "" {
		t.Fatal("a chave não foi salva no armazenamento separado", err)
	}
	test.Tap(form.save)
	h.await(func() bool { return h.d.settingsForm == nil })
	preferences, err := os.ReadFile(filepath.Join(h.d.directory, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(preferences), "fictitious-desktop-secret") || verifications.Load() != 0 {
		t.Fatal("salvar API gravou a chave no JSON ou exigiu autenticação AWS")
	}
	if _, err := vault.Read((apiconfig.Connection{Provider: apiconfig.Generic, Endpoint: "https://other.example/v1"}).CredentialTarget()); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("o segredo de um destino vazou para outro")
	}
	h.d.openAudioAPISettings()
	form = h.d.settingsForm
	h.await(func() bool { return !form.loading })
	f = form.audioAPI.manager
	test.Tap(f.remove)
	h.await(func() bool { return !f.busy })
	if _, err := vault.Read(connection.CredentialTarget()); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("remover a chave pelo gestor visível não funcionou")
	}
}

func TestFailedSecureSaveDoesNotPersistPlaintextOrPretendSuccess(t *testing.T) {
	vault := &testVault{writeErr: errors.New("falha no Windows")}
	h := newHarness(t, nil, func(deps *dependencies) { deps.vault = vault })
	h.d.openAudioAPISettings()
	form := h.d.settingsForm
	h.await(func() bool { return !form.loading })
	f := form.audioAPI.manager
	form.audioAPI.model.SetText("audio-model")
	f.key.SetText("fictitious-failed-secret")
	test.Tap(f.saveKey)
	h.await(func() bool { return !f.busy })
	test.Tap(form.save)
	if h.d.settingsForm == nil || !form.errorLabel.Visible() ||
		!strings.Contains(f.status.Text, "Não foi possível") ||
		strings.Contains(f.status.Text+form.errorLabel.Text, "fictitious-failed-secret") {
		t.Fatal("a falha de armazenamento foi apresentada como sucesso ou expôs o segredo")
	}
}

func TestAudioModelCatalogIsVisibleAndUpdatesFlowModel(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.Path != "/v1/models" || request.Header.Get("Authorization") != "Bearer synthetic-catalog-key" {
			t.Error("a consulta não usou o endpoint e a credencial da conexão ativa")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":[{"id":"text-only","architecture":{"output_modalities":["text"]}},{"id":"audio-transcribe","architecture":{"output_modalities":["transcription"]}}]}`))
	}))
	defer server.Close()
	connection := apiconfig.SavedConnection{
		ID: strings.Repeat("9", 32), Name: "Catálogo de teste", Provider: apiconfig.Generic,
		Endpoint: server.URL + "/v1", Text: true, Audio: true,
	}
	vault := &testVault{values: map[string]string{connection.Connection().CredentialTarget(): "synthetic-catalog-key"}}
	h := newHarness(t, nil, func(deps *dependencies) { deps.vault = vault })
	h.d.settings.APIConnections = []apiconfig.SavedConnection{connection}
	h.d.settings.AudioAPI = apiconfig.Audio{Connection: connection.Connection(), Model: "manual-model"}
	h.d.openAudioAPISettings()
	main := h.d.settingsForm
	h.await(func() bool { return !main.loading })
	main.sections.selectID("audio/api")
	form := main.audioAPI
	objects := test.LaidOutObjects(main.sections.pages["audio/api"])
	for _, expected := range []fyne.CanvasObject{form.catalog, form.model, form.status} {
		found := false
		for _, object := range objects {
			if object == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("a página visível de Áudio/API não contém a consulta, o modelo e o resultado")
		}
	}
	test.Tap(form.catalog)
	h.await(func() bool { return !form.busy })
	if requests.Load() != 1 || form.model.Text != "manual-model" {
		t.Fatal("a consulta não ocorreu ou trocou o ID manual antes da seleção")
	}
	test.Tap(form.model.ActionItem.(fyne.Tappable))
	menu, ok := h.d.window.Canvas().Focused().(*widget.PopUpMenu)
	if !ok || len(menu.Items) != 1 {
		t.Fatal("o seletor visível não oferece somente o modelo de transcrição do catálogo")
	}
	test.Tap(menu.Items[0].(fyne.Tappable))
	if form.model.Text != "audio-transcribe" {
		t.Fatal("o resultado do catálogo não pode ser selecionado no fluxo de áudio")
	}
	if !strings.Contains(form.status.Text, "Conexão testada") {
		t.Fatal("o resultado da consulta não ficou visível na página do fluxo")
	}
}

func TestEditingSavedTextProfileAppliesOnlyWhenReSelectedAndNewTabsInherit(t *testing.T) {
	h := newHarness(t, nil, nil)
	first := h.d.document
	options := processing.Default()
	options.ProfileID, options.Provider, options.ModelID = "saved-profile", "api", "old-model"
	options.API = apiconfig.Connection{Provider: apiconfig.Generic, Endpoint: "https://text.example/v1"}
	options = options.WithLegacyMerge(h.d.settings.Merge)
	h.d.settings.TextProfiles = []processing.Profile{{ID: options.ProfileID, Name: "Meu perfil", Options: options}}
	h.d.changeProcessingOptions(first, options)
	h.settle()
	second := h.d.addDocument()
	if second.processOptions != options || second.editor.Text != "" || len(second.processHistory) != 0 {
		t.Fatal("a nova aba não herdou somente as opções da última do grupo")
	}
	h.d.openProcessingSettings(first)
	form := h.d.processingForm
	form.model.SetText("new-model")
	test.Tap(form.saveProfile)
	h.await(func() bool { return h.d.processingForm == nil })
	if h.d.settings.TextProfiles[0].Options.ModelID != "new-model" ||
		first.processOptions.ModelID != "new-model" || second.processOptions.ModelID != "old-model" {
		t.Fatal("editar o perfil atualizou imediatamente outra aba")
	}
	second.processModel.SetSelected("Perfil · Meu perfil")
	h.settle()
	if second.processOptions.ModelID != "new-model" {
		t.Fatal("selecionar novamente não reaplicou o perfil salvo")
	}
	third := h.d.addDocument()
	if third.processOptions != second.processOptions || third.editor.Text != "" ||
		third.processEditor.Text != "" || len(third.promptSteps) != 0 {
		t.Fatal("a herança copiou texto/histórico ou usou outra aba")
	}
}
