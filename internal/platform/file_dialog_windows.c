//go:build windows && cgo

#define COBJMACROS
#include <windows.h>
#include <ole2.h>
#include <shobjidl.h>
#include <shlguid.h>
#include <servprov.h>
#include <initguid.h>
#include <propkey.h>
#include <stdlib.h>
#include "file_dialog_windows.h"

struct TalkerFileDialogState {
    LONG canceled;
    void *volatile window;
};

typedef struct {
    IFileDialogEvents iface;
    LONG references;
    int newest;
    TalkerFileDialogState *state;
} TalkerFileEvents;

TalkerFileDialogState *talkerNewFileDialogState(void) {
    return calloc(1, sizeof(TalkerFileDialogState));
}

void talkerCancelFileDialog(TalkerFileDialogState *state) {
    InterlockedExchange(&state->canceled, 1);
    HWND window = InterlockedCompareExchangePointer(&state->window, NULL, NULL);
    if (window) PostMessageW(window, WM_CLOSE, 0, 0);
}

void talkerFreeFileDialogState(TalkerFileDialogState *state) { free(state); }
void talkerFreeFilePath(wchar_t *path) { CoTaskMemFree(path); }

static HRESULT STDMETHODCALLTYPE talkerFileQuery(IFileDialogEvents *self, REFIID iid, void **result) {
    *result = NULL;
    if (!IsEqualIID(iid, &IID_IUnknown) && !IsEqualIID(iid, &IID_IFileDialogEvents))
        return E_NOINTERFACE;
    *result = self;
    IFileDialogEvents_AddRef(self);
    return S_OK;
}
static ULONG STDMETHODCALLTYPE talkerFileAddRef(IFileDialogEvents *self) {
    return InterlockedIncrement(&((TalkerFileEvents *)self)->references);
}
static ULONG STDMETHODCALLTYPE talkerFileRelease(IFileDialogEvents *self) {
    ULONG remaining = InterlockedDecrement(&((TalkerFileEvents *)self)->references);
    if (!remaining) free(self);
    return remaining;
}
static HRESULT STDMETHODCALLTYPE talkerFileOk(IFileDialogEvents *self, IFileDialog *dialog) { return S_OK; }
static HRESULT STDMETHODCALLTYPE talkerFileFolderChanging(
    IFileDialogEvents *self, IFileDialog *dialog, IShellItem *folder) { return S_OK; }
static HRESULT STDMETHODCALLTYPE talkerFileSelection(IFileDialogEvents *self, IFileDialog *dialog) { return S_OK; }
static HRESULT STDMETHODCALLTYPE talkerFileType(IFileDialogEvents *self, IFileDialog *dialog) { return S_OK; }
static HRESULT STDMETHODCALLTYPE talkerFileShare(
    IFileDialogEvents *self, IFileDialog *dialog, IShellItem *item, FDE_SHAREVIOLATION_RESPONSE *response) {
    *response = FDESVR_DEFAULT;
    return S_OK;
}
static HRESULT STDMETHODCALLTYPE talkerFileOverwrite(
    IFileDialogEvents *self, IFileDialog *dialog, IShellItem *item, FDE_OVERWRITE_RESPONSE *response) {
    *response = FDEOR_DEFAULT;
    return S_OK;
}

static void talkerFileDetails(IFileDialog *dialog) {
    IServiceProvider *services = NULL;
    IShellBrowser *browser = NULL;
    IShellView *view = NULL;
    IFolderView2 *folder = NULL;
    IColumnManager *columns = NULL;
    if (FAILED(IFileDialog_QueryInterface(dialog, &IID_IServiceProvider, (void **)&services))) return;
    if (SUCCEEDED(IServiceProvider_QueryService(services, &SID_STopLevelBrowser,
                                               &IID_IShellBrowser, (void **)&browser)) &&
        SUCCEEDED(IShellBrowser_QueryActiveShellView(browser, &view)) &&
        SUCCEEDED(IShellView_QueryInterface(view, &IID_IFolderView2, (void **)&folder))) {
        IFolderView2_SetCurrentViewMode(folder, FVM_DETAILS);
        if (SUCCEEDED(IShellView_QueryInterface(view, &IID_IColumnManager, (void **)&columns))) {
            PROPERTYKEY keys[] = {PKEY_ItemNameDisplay, PKEY_DateModified, PKEY_Size};
            IColumnManager_SetColumns(columns, keys, 3);
        }
        SORTCOLUMN sort = {PKEY_DateModified, SORT_DESCENDING};
        IFolderView2_SetSortColumns(folder, &sort, 1);
    }
    if (columns) IColumnManager_Release(columns);
    if (folder) IFolderView2_Release(folder);
    if (view) IShellView_Release(view);
    if (browser) IShellBrowser_Release(browser);
    IServiceProvider_Release(services);
}

static HRESULT STDMETHODCALLTYPE talkerFileFolderChanged(IFileDialogEvents *self, IFileDialog *dialog) {
    TalkerFileEvents *events = (TalkerFileEvents *)self;
    IOleWindow *window = NULL;
    if (SUCCEEDED(IFileDialog_QueryInterface(dialog, &IID_IOleWindow, (void **)&window))) {
        HWND handle = NULL;
        if (SUCCEEDED(IOleWindow_GetWindow(window, &handle)))
            InterlockedExchangePointer(&events->state->window, handle);
        IOleWindow_Release(window);
    }
    if (InterlockedCompareExchange(&events->state->canceled, 0, 0)) {
        IFileDialog_Close(dialog, HRESULT_FROM_WIN32(ERROR_CANCELLED));
        return S_OK;
    }
    if (events->newest) talkerFileDetails(dialog);
    return S_OK;
}

static IFileDialogEventsVtbl talkerFileEventsTable = {
    talkerFileQuery, talkerFileAddRef, talkerFileRelease,
    talkerFileOk, talkerFileFolderChanging, talkerFileFolderChanged,
    talkerFileSelection, talkerFileShare, talkerFileType, talkerFileOverwrite
};

int32_t talkerChooseFile(
    TalkerFileDialogState *state, uintptr_t owner, int save, int newest,
    const wchar_t *title, const wchar_t *folder, const wchar_t *filename,
    const wchar_t *extension, const TalkerFileFilter *filters, unsigned count,
    wchar_t **result) {
    *result = NULL;
    HRESULT initialized = CoInitializeEx(NULL, COINIT_APARTMENTTHREADED | COINIT_DISABLE_OLE1DDE);
    if (FAILED(initialized)) return initialized;
    IFileDialog *dialog = NULL;
    HRESULT status = CoCreateInstance(save ? &CLSID_FileSaveDialog : &CLSID_FileOpenDialog,
        NULL, CLSCTX_INPROC_SERVER, &IID_IFileDialog, (void **)&dialog);
    TalkerFileEvents *events = NULL;
    DWORD cookie = 0;
    int advised = 0;
    if (SUCCEEDED(status)) {
        FILEOPENDIALOGOPTIONS options = FOS_FORCEFILESYSTEM | FOS_PATHMUSTEXIST |
            FOS_NOCHANGEDIR | FOS_DONTADDTORECENT;
        options |= save ? FOS_OVERWRITEPROMPT : FOS_FILEMUSTEXIST;
        status = IFileDialog_SetOptions(dialog, options);
    }
    if (SUCCEEDED(status) && count)
        status = IFileDialog_SetFileTypes(dialog, count, (const COMDLG_FILTERSPEC *)filters);
    if (SUCCEEDED(status) && title && *title) status = IFileDialog_SetTitle(dialog, title);
    if (SUCCEEDED(status) && filename && *filename) status = IFileDialog_SetFileName(dialog, filename);
    if (SUCCEEDED(status) && extension && *extension) status = IFileDialog_SetDefaultExtension(dialog, extension);
    // Windows remembers this app's last folder without sharing the Explorer
    // view preference. DefaultFolder is used only before the first selection.
    static const GUID openClient = {0xe7c98824, 0x70d4, 0x428b, {0xa5,0x40,0x36,0x78,0x6f,0x12,0x9c,0xd5}};
    static const GUID saveClient = {0xabce9359, 0xc5b6, 0x4bcb, {0xb7,0xa3,0x6b,0x5f,0x22,0xb2,0x51,0x29}};
    static const GUID testClient = {0x7e4c87d3, 0x6048, 0x4f46, {0x9e,0x4f,0xa2,0xf8,0x9b,0x6b,0x7f,0x30}};
    if (SUCCEEDED(status)) status = IFileDialog_SetClientGuid(dialog, owner ? (save ? &saveClient : &openClient) : &testClient);
    if (SUCCEEDED(status) && !owner) IFileDialog_ClearClientData(dialog);
    if (SUCCEEDED(status) && folder && *folder) {
        IShellItem *item = NULL;
        if (SUCCEEDED(SHCreateItemFromParsingName(folder, NULL, &IID_IShellItem, (void **)&item))) {
            if (owner) IFileDialog_SetDefaultFolder(dialog, item);
            else IFileDialog_SetFolder(dialog, item);
            IShellItem_Release(item);
        }
    }
    if (SUCCEEDED(status)) {
        events = calloc(1, sizeof(TalkerFileEvents));
        if (!events) status = E_OUTOFMEMORY;
        else {
            events->iface.lpVtbl = &talkerFileEventsTable;
            events->references = 1;
            events->newest = newest;
            events->state = state;
            status = IFileDialog_Advise(dialog, &events->iface, &cookie);
            advised = SUCCEEDED(status);
        }
    }
    if (SUCCEEDED(status) && !InterlockedCompareExchange(&state->canceled, 0, 0))
        status = IFileDialog_Show(dialog, (HWND)owner);
    else if (SUCCEEDED(status)) status = HRESULT_FROM_WIN32(ERROR_CANCELLED);
    InterlockedExchangePointer(&state->window, NULL);
    if (SUCCEEDED(status)) {
        IShellItem *item = NULL;
        status = IFileDialog_GetResult(dialog, &item);
        if (SUCCEEDED(status)) {
            status = IShellItem_GetDisplayName(item, SIGDN_FILESYSPATH, result);
            IShellItem_Release(item);
        }
    }
    if (advised) IFileDialog_Unadvise(dialog, cookie);
    if (events) IFileDialogEvents_Release(&events->iface);
    if (dialog && !owner) IFileDialog_ClearClientData(dialog);
    if (dialog) IFileDialog_Release(dialog);
    CoUninitialize();
    return status;
}
