#ifndef TALKER_FILE_DIALOG_H
#define TALKER_FILE_DIALOG_H

#include <stdint.h>
#include <wchar.h>

typedef struct TalkerFileDialogState TalkerFileDialogState;

typedef struct {
    const wchar_t *name;
    const wchar_t *pattern;
} TalkerFileFilter;

TalkerFileDialogState *talkerNewFileDialogState(void);
void talkerCancelFileDialog(TalkerFileDialogState *state);
void talkerFreeFileDialogState(TalkerFileDialogState *state);
void talkerFreeFilePath(wchar_t *path);
int32_t talkerChooseFile(
    TalkerFileDialogState *state, uintptr_t owner, int save, int newest,
    const wchar_t *title, const wchar_t *folder, const wchar_t *filename,
    const wchar_t *extension, const TalkerFileFilter *filters, unsigned count,
    wchar_t **result);

#endif
