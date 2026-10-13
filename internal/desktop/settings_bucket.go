package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"ai-talker/internal/cloud"
	"ai-talker/internal/config"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

func (d *desktop) buildBucketSettings(form *settingsForm, settings config.Settings) fyne.CanvasObject {
	form.cloudBucket = widget.NewEntry()
	form.cloudBucket.PlaceHolder = "Nome do bucket, sem s3:// ou caminho"
	form.cloudBucket.SetText(settings.Cloud.S3Bucket)
	form.cloudUseBucket = widget.NewCheck(uiText("Usar bucket S3"), nil)
	form.cloudUseBucket.SetChecked(settings.Cloud.S3Enabled())
	form.cloudBucketResult = widget.NewLabel("")
	form.cloudBucketResult.Wrapping = fyne.TextWrapWord
	form.cloudBucketResult.Hide()
	form.cloudTestBucket = widget.NewButton(uiText("Testar bucket"), d.guard(func() { d.testBucketFromForm(form) }))
	refresh := func() {
		setEnabled(form.cloudBucket, form.cloudUseBucket.Checked)
		setEnabled(form.cloudTestBucket, form.cloudUseBucket.Checked || form.bucketCancel != nil)
	}
	form.cloudUseBucket.OnChanged = func(bool) {
		if form.bucketCancel != nil {
			form.bucketCancel()
		}
		refresh()
	}
	refresh()
	hint := widget.NewLabel("Desabilitar conserva o nome salvo. O teste usa o perfil e a região acima e verifica acesso, região, envio, leitura e limpeza de um objeto sintético. Não inicia uma transcrição.")
	hint.Wrapping = fyne.TextWrapWord
	return container.NewVBox(
		widget.NewSeparator(), settingLabel(uiText("S3 · Amazon Transcribe por arquivo")),
		form.cloudUseBucket, form.cloudBucket, d.smallText(hint),
		form.cloudTestBucket, d.smallText(form.cloudBucketResult),
	)
}

func (d *desktop) testBucketFromForm(form *settingsForm) {
	if d.settingsForm != form {
		return
	}
	if form.bucketCancel != nil {
		form.bucketCancel()
		form.cloudTestBucket.Disable()
		form.cloudBucketResult.SetText("Cancelando e conferindo a limpeza do objeto temporário…")
		return
	}
	settings := cloneSettings(d.settings)
	settings.Profile = strings.TrimSpace(form.profile.Text)
	if settings.Profile == automaticProfile || settings.Profile == "" {
		settings.Profile = form.defaultProfile
	}
	settings.Region = strings.TrimSpace(form.region.Text)
	settings.Cloud.S3Bucket = strings.TrimSpace(form.cloudBucket.Text)
	useS3 := form.cloudUseBucket.Checked
	settings.Cloud.UseS3 = &useS3
	if !useS3 || settings.Cloud.S3Bucket == "" {
		form.cloudBucketResult.Importance = widget.DangerImportance
		form.cloudBucketResult.SetText("Marque Usar bucket S3 e informe o nome do bucket.")
		form.cloudBucketResult.Show()
		return
	}
	if err := settings.Cloud.Validate(); err != nil {
		form.cloudBucketResult.Importance = widget.DangerImportance
		form.cloudBucketResult.SetText(err.Error())
		form.cloudBucketResult.Show()
		return
	}
	ctx, cancel := context.WithCancel(d.ctx)
	form.bucketCancel = cancel
	form.bucketSerial++
	serial := form.bucketSerial
	form.cloudBucketResult.Importance = widget.MediumImportance
	form.cloudBucketResult.SetText("Verificando perfil, acesso, região, envio, leitura e limpeza…")
	form.cloudBucketResult.Show()
	form.cloudTestBucket.SetText("Cancelar teste")
	var result cloud.BucketCheck
	verify := d.deps.verifyBucket
	if verify == nil {
		verify = cloud.TestBucket
	}
	d.operation(func() error {
		defer cancel()
		var err error
		result, err = verify(ctx, settings)
		return err
	}, func(err error) {
		if d.settingsForm != form || form.bucketSerial != serial {
			return
		}
		form.bucketCancel = nil
		form.cloudTestBucket.SetText("Testar bucket")
		setEnabled(form.cloudTestBucket, form.cloudUseBucket.Checked)
		form.cloudBucketResult.Importance = widget.SuccessImportance
		if err == nil && !result.Cleaned {
			err = errors.New("a AWS não confirmou a limpeza do objeto temporário")
		}
		if err != nil {
			form.cloudBucketResult.Importance = widget.DangerImportance
			form.cloudBucketResult.SetText("Falha ao testar o bucket: " + err.Error())
			if errors.Is(err, context.Canceled) && result.Cleaned {
				form.cloudBucketResult.Importance = widget.MediumImportance
				form.cloudBucketResult.SetText("Teste cancelado; objeto temporário removido.")
			}
			return
		}
		form.cloudBucketResult.SetText(fmt.Sprintf(
			"Bucket funcionando para este perfil: %s · %s. Envio, leitura e limpeza confirmados.\n"+
				"A permissão do serviço Amazon Transcribe e sua chave KMS ainda precisam ser conferidas em uma transcrição.",
			result.Bucket, result.Region,
		))
	})
}
