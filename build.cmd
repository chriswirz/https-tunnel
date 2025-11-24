@echo off
setlocal enabledelayedexpansion

rem Build and test https-tunnel on Windows.
rem
rem   build.cmd            frontend export, gofmt, vet, test, then https-tunnel.exe
rem   build.cmd web        build the Next.js frontend into web\out only
rem   build.cmd quick      Go build only, reusing whatever is in web\out
rem   build.cmd test       run the Go tests only
rem   build.cmd dev        run the Go server and `next dev` side by side
rem   build.cmd examples   build the example programs into dist\examples\
rem   build.cmd linux      cross compile https-tunnel-linux-amd64 for the server
rem   build.cmd run        build, then run against config.json (both sections)
rem   build.cmd smoke      build, then run a full local client plus server test
rem   build.cmd clean      remove build output

cd /d "%~dp0"

set BINARY=https-tunnel.exe
set VERSION=dev
for /f "delims=" %%v in ('git describe --tags --always --dirty 2^>nul') do set VERSION=%%v
set LDFLAGS=-s -w -X main.version=%VERSION%

where go >nul 2>nul
if errorlevel 1 (
    echo ERROR: go is not on PATH. Install Go from https://go.dev/dl/ and reopen this shell.
    exit /b 1
)

set TARGET=%~1
if "%TARGET%"=="" set TARGET=all

if /i "%TARGET%"=="all"   goto :all
if /i "%TARGET%"=="web"   goto :web
if /i "%TARGET%"=="dev"   goto :dev
if /i "%TARGET%"=="quick" goto :build
if /i "%TARGET%"=="test"  goto :test
if /i "%TARGET%"=="linux" goto :linux
if /i "%TARGET%"=="run"   goto :run
if /i "%TARGET%"=="smoke" goto :smoke
if /i "%TARGET%"=="clean" goto :clean
if /i "%TARGET%"=="examples" goto :examples

echo ERROR: unknown target "%TARGET%".
echo Valid targets: all, web, quick, test, dev, examples, linux, run, smoke, clean
exit /b 1

:all
call :web      || exit /b 1
call :vet      || exit /b 1
call :test     || exit /b 1
call :build    || exit /b 1
call :examples || exit /b 1
goto :eof

rem The examples are the documentation for the tunnelclient library, so they are
rem compiled alongside everything else rather than left to rot.
:examples
echo == building examples ==
if not exist dist\examples mkdir dist\examples
for /d %%d in (examples\*) do (
    rem examples\ also holds compose files, which are not Go packages.
    if exist "examples\%%~nxd\*.go" (
    go build -trimpath -o "dist\examples\%%~nxd.exe" ".\examples\%%~nxd"
    if errorlevel 1 exit /b 1
    echo   dist\examples\%%~nxd.exe
    )
)
goto :eof

rem The Go binary embeds web\out, so the frontend is built first and the Go
rem build simply picks up whatever is there.
:web
where npm >nul 2>nul
if errorlevel 1 (
    echo ERROR: npm is not on PATH. Install Node.js from https://nodejs.org/ and reopen this shell.
    exit /b 1
)
echo == building frontend (Next.js static export) ==
pushd web
if not exist node_modules (
    call npm install --no-audit --no-fund
    if errorlevel 1 ( popd & exit /b 1 )
)
call npm run build
if errorlevel 1 ( popd & exit /b 1 )
popd
goto :eof

rem Two windows: the Go server on 8080 with the API, and `next dev` on 3000 with
rem hot reload, proxying /api and /openapi.json back to the Go server.
:dev
if not exist config.json (
    echo ERROR: no config.json. Run https-tunnel --example-config ^> config.json first.
    exit /b 1
)
echo == starting the Go server on 8080 ==
start "https-tunnel server" cmd /k "go run . --config config.json server"
echo == starting next dev on 3000 ==
pushd web
if not exist node_modules call npm install --no-audit --no-fund
start "https-tunnel web" cmd /k "npm run dev"
popd
echo.
echo   api  http://localhost:8080/
echo   web  http://localhost:3000/
goto :eof

:vet
echo == gofmt ==
set UNFORMATTED=
for /f "delims=" %%f in ('gofmt -l . 2^>nul') do set UNFORMATTED=!UNFORMATTED! %%f
if not "!UNFORMATTED!"=="" (
    echo ERROR: these files need gofmt -w:!UNFORMATTED!
    exit /b 1
)
echo == go vet ==
go vet ./...
if errorlevel 1 exit /b 1
goto :eof

:test
echo == go test ==
go test ./...
if errorlevel 1 exit /b 1
goto :eof

:build
echo == go build %VERSION% ==
go build -ldflags "%LDFLAGS%" -o %BINARY% .
if errorlevel 1 exit /b 1
echo built %CD%\%BINARY%
goto :eof

:linux
call :web || exit /b 1
echo == cross compile linux/amd64 ==
set GOOS=linux
set GOARCH=amd64
go build -ldflags "%LDFLAGS%" -o https-tunnel-linux-amd64 .
set BUILDERR=%errorlevel%
set GOOS=
set GOARCH=
if not "%BUILDERR%"=="0" exit /b %BUILDERR%
echo built %CD%\https-tunnel-linux-amd64
goto :eof

:run
call :build || exit /b 1
if not exist config.json (
    echo ERROR: no config.json. Copy config.example.json and fill it in.
    exit /b 1
)
%BINARY% -c config.json
goto :eof

rem Runs a server and a client against each other on loopback, with no DNS and
rem no nginx, by sending the tunnel hostname in the Host header. Needs curl,
rem which ships with Windows 10 and later.
:smoke
call :build || exit /b 1
where curl >nul 2>nul
if errorlevel 1 (
    echo ERROR: curl is not on PATH, cannot run the smoke test.
    exit /b 1
)

set SMOKE=%TEMP%\https-tunnel-smoke
if exist "%SMOKE%" rd /s /q "%SMOKE%"
mkdir "%SMOKE%"

echo == writing smoke fixtures to %SMOKE% ==
> "%SMOKE%\config.json" (
    echo {
    echo   "client": {
    echo     "api_key": "smoke-key",
    echo     "server_url": "http://127.0.0.1:18080",
    echo     "local_port": 18756
    echo   },
    echo   "server": {
    echo     "port": 18080,
    echo     "addr": "127.0.0.1",
    echo     "base_domain": "smoke.test",
    echo     "public_scheme": "http",
    echo     "api_keys": [{ "name": "smoke", "key": "smoke-key" }],
    echo     "state_file": "%SMOKE:\=/%/sessions.json"
    echo   }
    echo }
)
> "%SMOKE%\local.go" (
    echo package main
    echo.
    echo import ^(
    echo 	"fmt"
    echo 	"io"
    echo 	"net/http"
    echo ^)
    echo.
    echo func main^(^) {
    echo 	http.HandleFunc^("/", func^(w http.ResponseWriter, r *http.Request^) {
    echo 		b, _ := io.ReadAll^(r.Body^)
    echo 		fmt.Fprintf^(w, "local server saw %%s %%s body=%%q\n", r.Method, r.URL.Path, string^(b^)^)
    echo 	}^)
    echo 	http.ListenAndServe^("127.0.0.1:18756", nil^)
    echo }
)
> "%SMOKE%\go.mod" (
    echo module smoketarget
    echo.
    echo go 1.25
)

pushd "%SMOKE%"
go build -o local.exe local.go
if errorlevel 1 ( popd & echo ERROR: could not build the smoke target. & exit /b 1 )
popd

echo == starting the fake local server on 18756 ==
start "https-tunnel-smoke-target" /min "%SMOKE%\local.exe"
echo == starting https-tunnel on 18080 ==
start "https-tunnel-smoke" /min cmd /c ""%CD%\%BINARY%" -c "%SMOKE%\config.json" > "%SMOKE%\out.log" 2>&1"

rem Give the tunnel a moment to register and attach.
ping -n 5 127.0.0.1 >nul

rem Pull the issued hostname out of the client's "tunnel established" log line.
rem cmd splits for-loop tokens on "=" as well as spaces, so the url= prefix is a
rem token of its own; match on the base domain instead and drop the scheme.
set HOSTHDR=
for /f "delims=" %%L in ('findstr /c:"tunnel established" "%SMOKE%\out.log"') do (
    for %%T in (%%L) do (
        set CANDIDATE=%%T
        if "!CANDIDATE:~0,7!"=="http://" (
            set REST=!CANDIDATE:~7!
            if not "!REST:smoke.test=!"=="!REST!" set HOSTHDR=!REST!
        )
    )
)
if "!HOSTHDR!"=="" (
    echo ERROR: the tunnel never came up. Log follows:
    type "%SMOKE%\out.log"
    goto :smoke_stop
)

echo.
echo == tunnel host is !HOSTHDR! ==
echo -- proxied request through the tunnel --
curl -s -H "Host: !HOSTHDR!" -d "hello" http://127.0.0.1:18080/mcp
echo.
echo -- control plane health --
curl -s http://127.0.0.1:18080/healthz
echo -- openapi spec --
curl -s http://127.0.0.1:18080/openapi.json | findstr /c:"\"openapi\""
echo -- session list --
curl -s -H "Authorization: Bearer smoke-key" http://127.0.0.1:18080/api/v1/sessions
echo.
echo -- web ui status --
curl -s -o nul -w "GET / -> %%{http_code}\n" http://127.0.0.1:18080/
curl -s -o nul -w "GET /sessions -> %%{http_code}\n" http://127.0.0.1:18080/sessions
curl -s -o nul -w "GET /docs -> %%{http_code}\n" http://127.0.0.1:18080/docs
echo -- unknown tunnel host should be 404 --
curl -s -o nul -w "GET nosuch.smoke.test -> %%{http_code}\n" -H "Host: nosuch.smoke.test" http://127.0.0.1:18080/

:smoke_stop
echo.
echo == stopping ==
taskkill /f /im %BINARY% >nul 2>nul
taskkill /f /im local.exe >nul 2>nul
echo smoke test finished. Log and fixtures are in %SMOKE%
goto :eof

:clean
if exist %BINARY% del %BINARY%
if exist https-tunnel-linux-amd64 del https-tunnel-linux-amd64
if exist web\out rd /s /q web\out
if exist web\.next rd /s /q web\.next
mkdir web\out 2>nul
type nul > web\out\.gitkeep
echo cleaned
goto :eof
