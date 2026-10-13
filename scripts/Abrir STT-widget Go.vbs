Option Explicit

Dim appShell, fileSystem, projectDirectory, appGo, command
Set appShell = CreateObject("WScript.Shell")
Set fileSystem = CreateObject("Scripting.FileSystemObject")

projectDirectory = fileSystem.GetParentFolderName(fileSystem.GetParentFolderName(WScript.ScriptFullName))
appShell.CurrentDirectory = projectDirectory
appGo = fileSystem.BuildPath(projectDirectory, "bin\STT-widget.exe")

If Not fileSystem.FileExists(appGo) Then
    appShell.Popup "Compile a versao Go com .\scripts\prepare-go.ps1 e .\scripts\build-go.ps1.", 0, "STT-widget Go", 48
    WScript.Quit 1
End If

command = Chr(34) & appGo & Chr(34)
appShell.Run command, 0, False
