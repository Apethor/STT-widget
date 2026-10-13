#define WIN32_LEAN_AND_MEAN
#define COBJMACROS
#include <windows.h>
#include <mmdeviceapi.h>
#include <audiopolicy.h>
#include <stdlib.h>
#include "volume_windows.h"

struct TalkerSessionVolume {
    ISimpleAudioVolume *volume;
    int initialized;
};

static const GUID talkerEnumeratorClass =
    {0xbcde0395, 0xe52f, 0x467c, {0x8e, 0x3d, 0xc4, 0x57, 0x92, 0x91, 0x69, 0x2e}};
static const GUID talkerEnumeratorID =
    {0xa95664d2, 0x9614, 0x4f35, {0xa7, 0x46, 0xde, 0x8d, 0xb6, 0x36, 0x17, 0xe6}};
static const GUID talkerManagerID =
    {0xbfa971f1, 0x4d5e, 0x40bb, {0x93, 0x5e, 0x96, 0x70, 0x39, 0xbf, 0xbe, 0xe4}};

int32_t talkerVolumeOpen(TalkerSessionVolume **result) {
    *result = NULL;
    TalkerSessionVolume *control = calloc(1, sizeof(*control));
    if (!control) return E_OUTOFMEMORY;
    HRESULT hr = CoInitializeEx(NULL, COINIT_MULTITHREADED);
    control->initialized = SUCCEEDED(hr);
    IMMDeviceEnumerator *enumerator = NULL;
    IMMDevice *device = NULL;
    IAudioSessionManager *manager = NULL;
    if (FAILED(hr) && hr != RPC_E_CHANGED_MODE) goto done;
    hr = CoCreateInstance(&talkerEnumeratorClass, NULL, CLSCTX_ALL,
                         &talkerEnumeratorID, (void **)&enumerator);
    if (FAILED(hr)) goto done;
    hr = IMMDeviceEnumerator_GetDefaultAudioEndpoint(enumerator, eRender, eMultimedia, &device);
    if (FAILED(hr)) goto done;
    hr = IMMDevice_Activate(device, &talkerManagerID, CLSCTX_ALL, NULL, (void **)&manager);
    if (FAILED(hr)) goto done;
    // NULL selects this process's default audio session, also used by WinMM/MCI.
    // The endpoint's global master volume is never requested or modified.
    hr = IAudioSessionManager_GetSimpleAudioVolume(manager, NULL, 0, &control->volume);
done:
    if (manager) IAudioSessionManager_Release(manager);
    if (device) IMMDevice_Release(device);
    if (enumerator) IMMDeviceEnumerator_Release(enumerator);
    if (FAILED(hr)) talkerVolumeClose(control);
    else *result = control;
    return hr;
}

int32_t talkerVolumeSet(TalkerSessionVolume *control, float volume) {
    if (!control || !control->volume || volume < 0 || volume > 1) return E_INVALIDARG;
    return ISimpleAudioVolume_SetMasterVolume(control->volume, volume, NULL);
}

int32_t talkerVolumeGet(TalkerSessionVolume *control, float *volume) {
    if (!control || !control->volume || !volume) return E_INVALIDARG;
    return ISimpleAudioVolume_GetMasterVolume(control->volume, volume);
}

void talkerVolumeClose(TalkerSessionVolume *control) {
    if (!control) return;
    if (control->volume) ISimpleAudioVolume_Release(control->volume);
    if (control->initialized) CoUninitialize();
    free(control);
}
