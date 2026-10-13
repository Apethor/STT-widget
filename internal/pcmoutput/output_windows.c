//go:build windows && cgo

#define COBJMACROS
#define INITGUID
#include "output_windows.h"
#include <mmdeviceapi.h>
#include <audioclient.h>
#include <stdlib.h>
#include <string.h>

struct TalkerResponseOutput {
    IMMDeviceEnumerator *enumerator;
    IMMDevice *device;
    IAudioClient *client;
    IAudioRenderClient *render;
    UINT32 capacity;
    int initialized;
    int paused;
};

HRESULT talkerResponseClose(TalkerResponseOutput *output) {
    if (!output) return S_OK;
    HRESULT hr = S_OK;
    if (output->client) hr = IAudioClient_Stop(output->client);
    if (output->render) IAudioRenderClient_Release(output->render);
    if (output->client) IAudioClient_Release(output->client);
    if (output->device) IMMDevice_Release(output->device);
    if (output->enumerator) IMMDeviceEnumerator_Release(output->enumerator);
    if (output->initialized) CoUninitialize();
    free(output);
    return hr;
}

HRESULT talkerResponseOpen(const wchar_t *endpoint, int rate, TalkerResponseOutput **result) {
    *result = NULL;
    TalkerResponseOutput *output = calloc(1, sizeof(*output));
    if (!output) return E_OUTOFMEMORY;
    HRESULT hr = CoInitializeEx(NULL, COINIT_MULTITHREADED);
    if (FAILED(hr)) { free(output); return hr; }
    output->initialized = 1;
    hr = CoCreateInstance(&CLSID_MMDeviceEnumerator, NULL, CLSCTX_ALL,
        &IID_IMMDeviceEnumerator, (void **)&output->enumerator);
    if (SUCCEEDED(hr)) {
        if (endpoint && *endpoint) hr = IMMDeviceEnumerator_GetDevice(output->enumerator, endpoint, &output->device);
        else hr = IMMDeviceEnumerator_GetDefaultAudioEndpoint(output->enumerator, eRender, eConsole, &output->device);
    }
    if (SUCCEEDED(hr)) hr = IMMDevice_Activate(output->device, &IID_IAudioClient, CLSCTX_ALL, NULL, (void **)&output->client);
    WAVEFORMATEX format = {0};
    format.wFormatTag = WAVE_FORMAT_PCM;
    format.nChannels = 1;
    format.nSamplesPerSec = rate;
    format.wBitsPerSample = 16;
    format.nBlockAlign = 2;
    format.nAvgBytesPerSec = rate * 2;
    if (SUCCEEDED(hr)) hr = IAudioClient_Initialize(output->client, AUDCLNT_SHAREMODE_SHARED,
        AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM | AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY,
        1000000, 0, &format, NULL);
    if (SUCCEEDED(hr)) hr = IAudioClient_GetBufferSize(output->client, &output->capacity);
    if (SUCCEEDED(hr)) hr = IAudioClient_GetService(output->client, &IID_IAudioRenderClient, (void **)&output->render);
    if (SUCCEEDED(hr)) hr = IAudioClient_Start(output->client);
    if (FAILED(hr)) { talkerResponseClose(output); return hr; }
    *result = output;
    return S_OK;
}

HRESULT talkerResponseWrite(TalkerResponseOutput *output, const BYTE *pcm, int bytes, int *written) {
    *written = 0;
    UINT32 padding = 0;
    HRESULT hr = IAudioClient_GetCurrentPadding(output->client, &padding);
    if (FAILED(hr)) return hr;
    if (padding > output->capacity || bytes < 0 || bytes % 2) return E_INVALIDARG;
    UINT32 frames = (UINT32)bytes / 2;
    if (frames > output->capacity - padding) frames = output->capacity - padding;
    if (!frames) return S_OK;
    BYTE *buffer = NULL;
    hr = IAudioRenderClient_GetBuffer(output->render, frames, &buffer);
    if (FAILED(hr)) return hr;
    memcpy(buffer, pcm, frames * 2);
    hr = IAudioRenderClient_ReleaseBuffer(output->render, frames, 0);
    if (SUCCEEDED(hr)) *written = (int)frames * 2;
    return hr;
}

HRESULT talkerResponsePending(TalkerResponseOutput *output, int *bytes) {
    UINT32 padding = 0;
    HRESULT hr = IAudioClient_GetCurrentPadding(output->client, &padding);
    if (SUCCEEDED(hr)) *bytes = (int)padding * 2;
    return hr;
}

HRESULT talkerResponseReset(TalkerResponseOutput *output) {
    HRESULT hr = IAudioClient_Stop(output->client);
    if (SUCCEEDED(hr)) hr = IAudioClient_Reset(output->client);
    if (SUCCEEDED(hr) && !output->paused) hr = IAudioClient_Start(output->client);
    return hr;
}

HRESULT talkerResponsePause(TalkerResponseOutput *output, int paused) {
    output->paused = paused;
    return paused ? IAudioClient_Stop(output->client) : IAudioClient_Start(output->client);
}
