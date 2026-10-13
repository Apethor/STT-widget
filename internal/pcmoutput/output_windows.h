#ifndef AI_TALKER_RESPONSE_OUTPUT_H
#define AI_TALKER_RESPONSE_OUTPUT_H
#include <windows.h>
typedef struct TalkerResponseOutput TalkerResponseOutput;
HRESULT talkerResponseOpen(const wchar_t *endpoint, int rate, TalkerResponseOutput **result);
HRESULT talkerResponseWrite(TalkerResponseOutput *output, const BYTE *pcm, int bytes, int *written);
HRESULT talkerResponseReset(TalkerResponseOutput *output);
HRESULT talkerResponsePause(TalkerResponseOutput *output, int paused);
HRESULT talkerResponsePending(TalkerResponseOutput *output, int *bytes);
HRESULT talkerResponseClose(TalkerResponseOutput *output);
#endif
