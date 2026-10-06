@echo off
rem Runs the app against the mock API (go run ./cmd/mockserver) with throwaway settings.
cd /d "%~dp0"
set PIXELDRAIN_API_BASE=http://127.0.0.1:8091/api
set PIXELDRAIN_DESKTOP_HOME=%~dp0.devhome
wails dev
