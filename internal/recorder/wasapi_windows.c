#define WIN32_LEAN_AND_MEAN
#define COBJMACROS
#define INITGUID
#include <windows.h>
#include <mmdeviceapi.h>
#include <audioclient.h>
#include <functiondiscoverykeys_devpkey.h>
#include <mmsystem.h>
#include <mmddk.h>
#include <stdlib.h>
#include <string.h>
#include "wasapi_windows.h"

// COM objects and their buffers never leave their owning native thread.
struct TalkerAudioInput {
    IMMDeviceEnumerator *enumerator;
    IMMDevice *device;
    IAudioClient *client;
    IAudioCaptureClient *capture;
    int initialized, started, sampleRate;
    uint64_t started100ns;
};

static uint64_t talkerClock100ns(void) {
    LARGE_INTEGER ticks, frequency;
    QueryPerformanceCounter(&ticks);
    QueryPerformanceFrequency(&frequency);
    return (uint64_t)(ticks.QuadPart / frequency.QuadPart) * 10000000 +
           (uint64_t)(ticks.QuadPart % frequency.QuadPart) * 10000000 / frequency.QuadPart;
}

static HRESULT talkerLegacyEndpoint(int legacy, wchar_t **id) {
    *id = NULL;
    DWORD bytes = 0;
    MMRESULT result = waveInMessage((HWAVEIN)(UINT_PTR)legacy,
        DRV_QUERYFUNCTIONINSTANCEIDSIZE, (DWORD_PTR)&bytes, 0);
    if (result || bytes < sizeof(wchar_t) || bytes > 65536)
        return HRESULT_FROM_WIN32(ERROR_NOT_FOUND);
    wchar_t *text = (wchar_t *)calloc(1, bytes + sizeof(wchar_t));
    if (!text) return E_OUTOFMEMORY;
    result = waveInMessage((HWAVEIN)(UINT_PTR)legacy,
        DRV_QUERYFUNCTIONINSTANCEID, (DWORD_PTR)text, bytes);
    if (result) { free(text); return HRESULT_FROM_WIN32(ERROR_NOT_FOUND); }
    *id = text;
    return S_OK;
}

int32_t talkerAudioDevices(int playback, TalkerAudioDevice **devices, int *count) {
    *devices = NULL;
    *count = 0;
    HRESULT hr = CoInitializeEx(NULL, COINIT_MULTITHREADED);
    int initialized = SUCCEEDED(hr);
    if (FAILED(hr) && hr != RPC_E_CHANGED_MODE) return hr;
    IMMDeviceEnumerator *enumerator = NULL;
    IMMDeviceCollection *collection = NULL;
    TalkerAudioDevice *output = NULL;
    hr = CoCreateInstance(&CLSID_MMDeviceEnumerator, NULL, CLSCTX_ALL,
                         &IID_IMMDeviceEnumerator, (void **)&enumerator);
    if (FAILED(hr)) goto done;
    hr = IMMDeviceEnumerator_EnumAudioEndpoints(enumerator,
            playback ? eRender : eCapture, DEVICE_STATE_ACTIVE, &collection);
    if (FAILED(hr)) goto done;
    UINT total = 0;
    hr = IMMDeviceCollection_GetCount(collection, &total);
    if (FAILED(hr)) goto done;
    if (total > 256) { hr = E_INVALIDARG; goto done; }
    output = (TalkerAudioDevice *)calloc(total ? total : 1, sizeof(TalkerAudioDevice));
    if (!output) { hr = E_OUTOFMEMORY; goto done; }
    for (UINT index = 0; index < total; index++) {
        IMMDevice *device = NULL;
        IPropertyStore *properties = NULL;
        LPWSTR id = NULL;
        PROPVARIANT name;
        PropVariantInit(&name);
        hr = IMMDeviceCollection_Item(collection, index, &device);
        if (SUCCEEDED(hr)) hr = IMMDevice_GetId(device, &id);
        if (SUCCEEDED(hr)) hr = IMMDevice_OpenPropertyStore(device, STGM_READ, &properties);
        if (SUCCEEDED(hr)) hr = IPropertyStore_GetValue(properties, &PKEY_Device_FriendlyName, &name);
        if (SUCCEEDED(hr)) {
            if (wcslen(id) >= 1024) hr = E_INVALIDARG;
            else {
                wcscpy(output[index].id, id);
                wcsncpy(output[index].name,
                        name.vt == VT_LPWSTR ? name.pwszVal : L"Dispositivo de audio", 255);
                output[index].legacyID = 100000 + (int)index;
                if (!playback) {
                    for (UINT legacy = 0; legacy < waveInGetNumDevs(); legacy++) {
                        wchar_t *legacyID = NULL;
                        if (SUCCEEDED(talkerLegacyEndpoint((int)legacy, &legacyID)) &&
                            wcscmp(legacyID, id) == 0) output[index].legacyID = (int)legacy;
                        free(legacyID);
                    }
                }
            }
        }
        PropVariantClear(&name);
        if (properties) IPropertyStore_Release(properties);
        CoTaskMemFree(id);
        if (device) IMMDevice_Release(device);
        if (FAILED(hr)) goto done;
    }
    *devices = output;
    *count = (int)total;
    output = NULL;
done:
    free(output);
    if (collection) IMMDeviceCollection_Release(collection);
    if (enumerator) IMMDeviceEnumerator_Release(enumerator);
    if (initialized) CoUninitialize();
    return hr;
}

void talkerAudioDevicesFree(TalkerAudioDevice *devices) { free(devices); }

int32_t talkerAudioOpen(const wchar_t *endpoint, int legacy, int loopback,
                      int sampleRate, TalkerAudioInput **result) {
    *result = NULL;
    TalkerAudioInput *input = (TalkerAudioInput *)calloc(1, sizeof(TalkerAudioInput));
    if (!input) return E_OUTOFMEMORY;
    HRESULT hr = CoInitializeEx(NULL, COINIT_MULTITHREADED);
    input->initialized = SUCCEEDED(hr);
    if (FAILED(hr) && hr != RPC_E_CHANGED_MODE) goto failed;
    hr = CoCreateInstance(&CLSID_MMDeviceEnumerator, NULL, CLSCTX_ALL,
            &IID_IMMDeviceEnumerator, (void **)&input->enumerator);
    if (FAILED(hr)) goto failed;
    wchar_t *legacyID = NULL;
    if (endpoint && endpoint[0]) {
        hr = IMMDeviceEnumerator_GetDevice(input->enumerator, endpoint, &input->device);
    } else if (!loopback && legacy >= 0) {
        hr = talkerLegacyEndpoint(legacy, &legacyID);
        if (SUCCEEDED(hr)) hr = IMMDeviceEnumerator_GetDevice(input->enumerator, legacyID, &input->device);
    } else {
        hr = IMMDeviceEnumerator_GetDefaultAudioEndpoint(input->enumerator,
                loopback ? eRender : eCapture, eConsole, &input->device);
    }
    free(legacyID);
    if (FAILED(hr)) goto failed;
    hr = IMMDevice_Activate(input->device, &IID_IAudioClient, CLSCTX_ALL, NULL, (void **)&input->client);
    if (FAILED(hr)) goto failed;
    WAVEFORMATEX format = {0};
    format.wFormatTag = WAVE_FORMAT_PCM;
    format.nChannels = 1;
    format.nSamplesPerSec = sampleRate;
    format.wBitsPerSample = 16;
    format.nBlockAlign = 2;
    format.nAvgBytesPerSec = sampleRate * 2;
    DWORD flags = AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM | AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY;
    if (loopback) flags |= AUDCLNT_STREAMFLAGS_LOOPBACK;
    hr = IAudioClient_Initialize(input->client, AUDCLNT_SHAREMODE_SHARED,
                                flags, 1000000, 0, &format, NULL);
    if (FAILED(hr)) goto failed;
    hr = IAudioClient_GetService(input->client, &IID_IAudioCaptureClient, (void **)&input->capture);
    if (FAILED(hr)) goto failed;
    input->sampleRate = sampleRate;
    *result = input;
    return S_OK;
failed:
    talkerAudioFree(input);
    return hr;
}

int32_t talkerAudioStart(TalkerAudioInput *input) {
    if (!input || input->started) return E_UNEXPECTED;
    HRESULT hr = IAudioClient_Start(input->client);
    if (SUCCEEDED(hr)) {
        input->started = 1;
        input->started100ns = talkerClock100ns();
    }
    return hr;
}

int32_t talkerAudioStop(TalkerAudioInput *input) {
    if (!input || !input->started) return S_OK;
    input->started = 0;
    return IAudioClient_Stop(input->client);
}

int32_t talkerAudioRead(TalkerAudioInput *input, unsigned char *pcm, int capacity,
                      int *length, uint64_t *position) {
    *length = 0;
    *position = UINT64_MAX;
    UINT32 frames = 0;
    HRESULT hr = IAudioCaptureClient_GetNextPacketSize(input->capture, &frames);
    if (FAILED(hr) || !frames) return hr;
    BYTE *source = NULL;
    DWORD flags = 0;
    UINT64 clock = 0;
    hr = IAudioCaptureClient_GetBuffer(input->capture, &source, &frames, &flags, NULL, &clock);
    if (FAILED(hr)) return hr;
    if ((uint64_t)frames * 2 > (uint64_t)capacity) {
        IAudioCaptureClient_ReleaseBuffer(input->capture, frames);
        return E_OUTOFMEMORY;
    }
    if (flags & AUDCLNT_BUFFERFLAGS_SILENT) memset(pcm, 0, frames * 2);
    else memcpy(pcm, source, frames * 2);
    *length = frames * 2;
    if (!(flags & AUDCLNT_BUFFERFLAGS_TIMESTAMP_ERROR) && clock >= input->started100ns)
        *position = (clock - input->started100ns) * input->sampleRate / 10000000;
    return IAudioCaptureClient_ReleaseBuffer(input->capture, frames);
}

void talkerAudioFree(TalkerAudioInput *input) {
    if (!input) return;
    talkerAudioStop(input);
    if (input->capture) IAudioCaptureClient_Release(input->capture);
    if (input->client) IAudioClient_Release(input->client);
    if (input->device) IMMDevice_Release(input->device);
    if (input->enumerator) IMMDeviceEnumerator_Release(input->enumerator);
    if (input->initialized) CoUninitialize();
    free(input);
}
