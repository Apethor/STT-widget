#ifndef AI_TALKER_WASAPI_H
#define AI_TALKER_WASAPI_H
#include <stdint.h>
#include <wchar.h>

typedef struct TalkerAudioInput TalkerAudioInput;
typedef struct {
    wchar_t id[1024];
    wchar_t name[256];
    int legacyID;
} TalkerAudioDevice;

int32_t talkerAudioDevices(int playback, TalkerAudioDevice **devices, int *count);
void talkerAudioDevicesFree(TalkerAudioDevice *devices);
int32_t talkerAudioOpen(const wchar_t *endpoint, int legacy, int loopback,
                      int sampleRate, TalkerAudioInput **result);
int32_t talkerAudioStart(TalkerAudioInput *input);
int32_t talkerAudioStop(TalkerAudioInput *input);
int32_t talkerAudioRead(TalkerAudioInput *input, unsigned char *pcm, int capacity,
                      int *length, uint64_t *position);
void talkerAudioFree(TalkerAudioInput *input);
#endif
