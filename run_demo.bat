@echo off
REM Build and run the hotreload demo on Windows
echo Building hotreload...
go build -o bin\hotreload.exe .\cmd\hotreload
if %ERRORLEVEL% NEQ 0 (
    echo Build failed!
    exit /b 1
)

echo Starting hotreload with testserver...
.\bin\hotreload.exe --root .\testserver --build "go build -o ./bin/testserver.exe ./testserver" --exec "./bin/testserver.exe"
