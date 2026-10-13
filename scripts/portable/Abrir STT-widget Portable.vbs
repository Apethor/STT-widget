Option Explicit

Dim appShell, fileSystem, appDirectory, executable, dataDirectory, environment, command
Set appShell = CreateObject("WScript.Shell")
Set fileSystem = CreateObject("Scripting.FileSystemObject")

appDirectory = fileSystem.GetParentFolderName(WScript.ScriptFullName)
executable = fileSystem.BuildPath(appDirectory, "STT-widget.exe")
dataDirectory = fileSystem.BuildPath(appDirectory, "dados")

If Not fileSystem.FileExists(executable) Then
    appShell.Popup "Extraia o ZIP completo antes de abrir o STT-widget Portable.", 0, "STT-widget Portable", 48
    WScript.Quit 1
End If
If Not fileSystem.FileExists(fileSystem.BuildPath(appDirectory, "tools\ffmpeg.exe")) Or _
        Not fileSystem.FileExists(fileSystem.BuildPath(appDirectory, "tools\ffprobe.exe")) Then
    appShell.Popup "A pasta tools esta incompleta. Extraia novamente o ZIP completo.", 0, "STT-widget Portable", 48
    WScript.Quit 1
End If

On Error Resume Next
If Not fileSystem.FolderExists(dataDirectory) Then
    fileSystem.CreateFolder dataDirectory
End If
If Err.Number <> 0 Then
    appShell.Popup "Nao foi possivel criar a pasta dados. Extraia o pacote em uma pasta onde voce possa gravar arquivos.", 0, "STT-widget Portable", 48
    WScript.Quit 1
End If
On Error GoTo 0

Set environment = appShell.Environment("Process")
environment("AI_TALKER_DATA_DIR") = dataDirectory
appShell.CurrentDirectory = appDirectory
command = Chr(34) & executable & Chr(34)

On Error Resume Next
appShell.Run command, 0, False
If Err.Number <> 0 Then
    appShell.Popup "Nao foi possivel abrir o STT-widget. Confira se o pacote foi extraido completamente.", 0, "STT-widget Portable", 48
    WScript.Quit 1
End If
