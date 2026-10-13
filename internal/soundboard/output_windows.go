package soundboard

/*
#cgo LDFLAGS: -lole32 -luuid -lavrt
#define WIN32_LEAN_AND_MEAN
#define COBJMACROS
#define INITGUID
#include <windows.h>
#include <mmdeviceapi.h>
#include <audioclient.h>
#include <avrt.h>
#include <stdlib.h>
#include <stdint.h>

typedef struct {
    IMMDeviceEnumerator *enumerator;
    IMMDevice *device;
    IAudioClient *client;
    IAudioRenderClient *render;
    UINT32 frames;
    REFERENCE_TIME latency;
    HANDLE event, scheduling;
    int initialized;
} SoundboardOutput;

static void soundboardClose(SoundboardOutput *out) {
    if (!out) return;
    if (out->client) IAudioClient_Stop(out->client);
    if (out->render) IAudioRenderClient_Release(out->render);
    if (out->client) IAudioClient_Release(out->client);
    if (out->device) IMMDevice_Release(out->device);
    if (out->enumerator) IMMDeviceEnumerator_Release(out->enumerator);
    if (out->event) CloseHandle(out->event);
    if (out->scheduling) AvRevertMmThreadCharacteristics(out->scheduling);
    if (out->initialized) CoUninitialize();
    free(out);
}

static HRESULT soundboardOpen(SoundboardOutput **result) {
    *result = NULL;
    SoundboardOutput *out = calloc(1, sizeof(SoundboardOutput));
    if (!out) return E_OUTOFMEMORY;
    HRESULT hr = CoInitializeEx(NULL, COINIT_MULTITHREADED);
    out->initialized = SUCCEEDED(hr);
    if (FAILED(hr) && hr != RPC_E_CHANGED_MODE) goto failed;
    hr = CoCreateInstance(&CLSID_MMDeviceEnumerator, NULL, CLSCTX_ALL,
                         &IID_IMMDeviceEnumerator, (void **)&out->enumerator);
    if (FAILED(hr)) goto failed;
    hr = IMMDeviceEnumerator_GetDefaultAudioEndpoint(out->enumerator, eRender, eConsole, &out->device);
    if (FAILED(hr)) goto failed;
    hr = IMMDevice_Activate(out->device, &IID_IAudioClient, CLSCTX_ALL, NULL, (void **)&out->client);
    if (FAILED(hr)) goto failed;
    WAVEFORMATEX format = {0};
    format.wFormatTag = WAVE_FORMAT_PCM;
    format.nChannels = 2;
    format.nSamplesPerSec = 48000;
    format.wBitsPerSample = 16;
    format.nBlockAlign = 4;
    format.nAvgBytesPerSec = 48000 * 4;
    hr = IAudioClient_Initialize(out->client, AUDCLNT_SHAREMODE_SHARED,
            AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM | AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY |
            AUDCLNT_STREAMFLAGS_EVENTCALLBACK,
            200000, 0, &format, NULL);
    if (FAILED(hr)) goto failed;
    out->event = CreateEventW(NULL, FALSE, FALSE, NULL);
    if (!out->event) { hr = HRESULT_FROM_WIN32(GetLastError()); goto failed; }
    hr = IAudioClient_SetEventHandle(out->client, out->event);
    if (FAILED(hr)) goto failed;
    hr = IAudioClient_GetBufferSize(out->client, &out->frames);
    if (FAILED(hr)) goto failed;
    hr = IAudioClient_GetService(out->client, &IID_IAudioRenderClient, (void **)&out->render);
    if (FAILED(hr)) goto failed;
    IAudioClient_GetStreamLatency(out->client, &out->latency);
    // Prefill silence once. All later clicks use this already-running stream.
    BYTE *buffer = NULL;
    hr = IAudioRenderClient_GetBuffer(out->render, out->frames, &buffer);
    if (FAILED(hr)) goto failed;
    hr = IAudioRenderClient_ReleaseBuffer(out->render, out->frames, AUDCLNT_BUFFERFLAGS_SILENT);
    if (FAILED(hr)) goto failed;
    hr = IAudioClient_Start(out->client);
    if (FAILED(hr)) goto failed;
    DWORD task = 0;
    out->scheduling = AvSetMmThreadCharacteristicsW(L"Audio", &task);
    if (out->scheduling) AvSetMmThreadPriority(out->scheduling, AVRT_PRIORITY_HIGH);
    *result = out;
    return S_OK;
failed:
    soundboardClose(out);
    return hr;
}

static HRESULT soundboardWait(SoundboardOutput *out) {
    // The bounded wait permits context teardown even after device removal.
    DWORD result = WaitForSingleObject(out->event, 100);
    if (result == WAIT_OBJECT_0 || result == WAIT_TIMEOUT) return S_OK;
    return HRESULT_FROM_WIN32(GetLastError());
}

static HRESULT soundboardBuffer(SoundboardOutput *out, int16_t **buffer, UINT32 *frames) {
    *frames = 0; *buffer = NULL;
    UINT32 padding = 0;
    HRESULT hr = IAudioClient_GetCurrentPadding(out->client, &padding);
    if (FAILED(hr)) return hr;
    if (padding > out->frames) return E_UNEXPECTED;
    UINT32 count = out->frames - padding;
    if (!count) return S_OK;
    hr = IAudioRenderClient_GetBuffer(out->render, count, (BYTE **)buffer);
    if (SUCCEEDED(hr)) *frames = count;
    return hr;
}
static HRESULT soundboardRelease(SoundboardOutput *out, UINT32 frames) {
    return IAudioRenderClient_ReleaseBuffer(out->render, frames, 0);
}
*/
import "C"

import (
	"fmt"
	"time"
	"unsafe"
)

type nativeRenderDevice struct{ output *C.SoundboardOutput }

func openRenderDevice() (renderDevice, error) {
	var output *C.SoundboardOutput
	if result := C.soundboardOpen(&output); result < 0 {
		return nil, fmt.Errorf("abrir a saída do painel de sons: WASAPI 0x%08x", uint32(result))
	}
	return &nativeRenderDevice{output: output}, nil
}

func (d *nativeRenderDevice) Write(fill func([]int16)) error {
	var buffer *C.int16_t
	var frames C.UINT32
	if result := C.soundboardBuffer(d.output, &buffer, &frames); result < 0 {
		return fmt.Errorf("obter saída do painel de sons: WASAPI 0x%08x", uint32(result))
	}
	if frames == 0 {
		return nil
	}
	fill(unsafe.Slice((*int16)(unsafe.Pointer(buffer)), int(frames)*outputChannels))
	if result := C.soundboardRelease(d.output, frames); result < 0 {
		return fmt.Errorf("enviar efeito à saída: WASAPI 0x%08x", uint32(result))
	}
	return nil
}

func (d *nativeRenderDevice) Wait() error {
	if result := C.soundboardWait(d.output); result < 0 {
		return fmt.Errorf("aguardar saída do painel de sons: WASAPI 0x%08x", uint32(result))
	}
	return nil
}

func (d *nativeRenderDevice) Latency() time.Duration {
	return time.Duration(d.output.latency)*100*time.Nanosecond +
		time.Duration(d.output.frames)*time.Second/outputRate
}

func (d *nativeRenderDevice) Close() error {
	C.soundboardClose(d.output)
	d.output = nil
	return nil
}
