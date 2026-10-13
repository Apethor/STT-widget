#include <stdint.h>

typedef struct TalkerSessionVolume TalkerSessionVolume;

int32_t talkerVolumeOpen(TalkerSessionVolume **result);
int32_t talkerVolumeSet(TalkerSessionVolume *control, float volume);
int32_t talkerVolumeGet(TalkerSessionVolume *control, float *volume);
void talkerVolumeClose(TalkerSessionVolume *control);
