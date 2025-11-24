#!/usr/bin/env bash
# Build and test https-tunnel on Linux or macOS.
#
#   ./build.sh            frontend export, gofmt, vet, test, then ./https-tunnel
#   ./build.sh web        build the Next.js frontend into web/out only
#   ./build.sh quick      Go build only, reusing whatever is in web/out
#   ./build.sh test       run the Go tests only
#   ./build.sh dev        run the Go server and `next dev` side by side
#   ./build.sh examples   build the example programs into dist/examples/
#   ./build.sh cross      build every release binary into dist/
#   ./build.sh run        build, then run against config.json
#   ./build.sh smoke      build, then run a full local client plus server test
#   ./build.sh clean      remove build output
set -euo pipefail

cd "$(dirname "$0")"

BINARY=https-tunnel
# Released builds are v<series>.<build number>, where the series comes from the
# VERSION file and the build number from the CI run. There is no build number on
# a developer machine, so the local stamp says so and carries the commit instead.
SERIES=$(cat VERSION 2>/dev/null || echo 0.1)
if [ -n "${GITHUB_RUN_NUMBER:-}" ]; then
  # Padded to four digits so versions sort identically as text and as numbers.
  VERSION="v${SERIES}.$(printf '%04d' "${GITHUB_RUN_NUMBER}")"
else
  VERSION="v${SERIES}.0-dev+$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
fi
LDFLAGS="-s -w -X main.version=${VERSION}"
# Windows needs the suffix even when cross compiling from here.
EXE_SUFFIX=""
case "${GOOS:-$(go env GOOS 2>/dev/null)}" in windows) EXE_SUFFIX=".exe" ;; esac

# Color only when a terminal is attached, so CI logs stay clean.
if [ -t 1 ]; then bold=$'\033[1m'; green=$'\033[32m'; red=$'\033[31m'; off=$'\033[0m'
else bold=''; green=''; red=''; off=''; fi
say()  { printf '%s==> %s%s\n' "$bold" "$1" "$off"; }
die()  { printf '%sERROR: %s%s\n' "$red" "$1" "$off" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "$1 is not on PATH. $2"; }

target_web() {
  need npm "Install Node.js 20 or newer from https://nodejs.org/"
  say "building frontend (Next.js static export)"
  ( cd web
    if [ ! -d node_modules ]; then
      # ci is reproducible and fails loudly on a lockfile mismatch, which is
      # what a build script wants; install is the fallback for a fresh tree.
      if [ -f package-lock.json ]; then npm ci --no-audit --no-fund
      else npm install --no-audit --no-fund; fi
    fi
    npm run build )
}

target_vet() {
  say "gofmt"
  unformatted=$(gofmt -l .)
  if [ -n "$unformatted" ]; then
    printf '%s\n' "$unformatted"
    die "the files above need gofmt -w"
  fi
  say "go vet"
  go vet ./...
}

target_test() {
  say "go test"
  go test ./...
}

target_build() {
  say "go build ${VERSION}"
  go build -trimpath -ldflags "$LDFLAGS" -o "$BINARY" .
  printf '%sbuilt %s/%s%s\n' "$green" "$PWD" "$BINARY" "$off"
}

# The examples are the documentation for the tunnelclient library, so they are
# compiled alongside everything else rather than left to rot.
target_examples() {
  say "building examples"
  mkdir -p dist/examples
  for dir in examples/*/; do
    # examples/ also holds compose files and other things that are not Go packages.
    ls "$dir"*.go >/dev/null 2>&1 || continue
    name=$(basename "$dir")
    go build -trimpath -o "dist/examples/${name}${EXE_SUFFIX}" "./${dir%/}"
    printf '  dist/examples/%s%s\n' "$name" "$EXE_SUFFIX"
  done
}

# The same set the release workflow publishes, so a local build can be compared
# against a downloaded one.
target_cross() {
  target_web
  mkdir -p dist
  for pair in linux/amd64 linux/arm64 windows/amd64 darwin/amd64 darwin/arm64; do
    goos=${pair%/*}; goarch=${pair#*/}
    ext=''; [ "$goos" = windows ] && ext='.exe'
    out="dist/${BINARY}-${goos}-${goarch}${ext}"
    say "building ${goos}/${goarch}"
    CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath -ldflags "$LDFLAGS" -o "$out" .
    ( cd dist && sha256sum "$(basename "$out")" > "$(basename "$out").sha256" )
  done
  ls -la dist
}

target_dev() {
  [ -f config.json ] || die "no config.json. Run ./https-tunnel --example-config > config.json first."
  need npm "Install Node.js 20 or newer from https://nodejs.org/"
  say "starting the Go server on 8080 and next dev on 3000"
  go run . --config config.json server &
  server_pid=$!
  ( cd web && [ -d node_modules ] || npm install --no-audit --no-fund )
  ( cd web && npm run dev ) &
  web_pid=$!
  trap 'kill $server_pid $web_pid 2>/dev/null || true' INT TERM EXIT
  printf '\n  api  http://localhost:8080/\n  web  http://localhost:3000/\n\n'
  wait
}

target_run() {
  target_build
  [ -f config.json ] || die "no config.json. Run ./${BINARY} --example-config > config.json first."
  exec "./${BINARY}" --config config.json
}

# Runs a client and a server against each other on loopback, with no DNS and no
# nginx, by sending the tunnel hostname in the Host header.
target_smoke() {
  target_build
  need curl "It is needed to drive the smoke test."

  smoke=$(mktemp -d)
  trap 'kill ${proxy_pid:-} ${target_pid:-} 2>/dev/null || true; rm -rf "$smoke"' EXIT

  mkdir -p "$smoke/site"
  echo '<h1>served from a directory</h1>' > "$smoke/site/index.html"

  cat > "$smoke/config.json" <<JSON
{
  "client": {
    "api_key": "smoke-key",
    "server_url": "http://127.0.0.1:18080",
    "local_port": 18756
  },
  "server": {
    "port": 18080,
    "addr": "127.0.0.1",
    "base_domain": "smoke.test",
    "public_scheme": "http",
    "api_keys": [{ "name": "smoke", "key": "smoke-key" }],
    "state_file": "$smoke/sessions.json"
  }
}
JSON

  say "starting a local target on 18756"
  python3 -m http.server 18756 --bind 127.0.0.1 --directory "$smoke/site" >/dev/null 2>&1 &
  target_pid=$!

  say "starting https-tunnel on 18080"
  "./${BINARY}" --config "$smoke/config.json" > "$smoke/out.log" 2>&1 &
  proxy_pid=$!

  for _ in $(seq 1 30); do
    grep -q "tunnel attached" "$smoke/out.log" && break
    sleep 1
  done

  host=$(grep -o 'url=http://[a-z0-9]*\.smoke\.test' "$smoke/out.log" | head -1 | sed 's|url=http://||')
  if [ -z "$host" ]; then
    cat "$smoke/out.log"
    die "the tunnel never came up"
  fi

  echo
  say "tunnel host is $host"
  echo "-- proxied request through the tunnel --"
  curl -s -H "Host: $host" http://127.0.0.1:18080/
  echo "-- control plane health --"
  curl -s http://127.0.0.1:18080/healthz
  echo "-- openapi spec --"
  curl -s http://127.0.0.1:18080/openapi.json | head -c 30; echo
  echo "-- web ui --"
  for path in / /sessions /docs /swagger; do
    curl -s -o /dev/null -w "GET %-10s -> %{http_code}\n" "$path" "http://127.0.0.1:18080$path"
  done
  echo "-- admin sign in --"
  curl -s -c "$smoke/jar" -X POST -d '{"username":"admin","password":"admin"}' \
    http://127.0.0.1:18080/api/v1/auth/login; echo
  curl -s -b "$smoke/jar" -c "$smoke/jar" -X POST \
    -d '{"current_password":"admin","new_username":"smoke-admin","new_password":"a-longer-secret"}' \
    http://127.0.0.1:18080/api/v1/auth/account; echo
  curl -s -b "$smoke/jar" -o /dev/null -w "GET /api/v1/sessions -> %{http_code}\n" \
    http://127.0.0.1:18080/api/v1/sessions
  echo "-- the renamed away account no longer signs in --"
  curl -s -o /dev/null -w "admin -> %{http_code}\n" -X POST \
    -d '{"username":"admin","password":"a-longer-secret"}' \
    http://127.0.0.1:18080/api/v1/auth/login
  echo "-- prune idle sessions --"
  curl -s -b "$smoke/jar" -X DELETE \
    "http://127.0.0.1:18080/api/v1/sessions?idle_for=1w"; echo
  echo "-- unknown tunnel host --"
  curl -s -o /dev/null -w "GET nosuch.smoke.test -> %{http_code}\n" \
    -H "Host: nosuch.smoke.test" http://127.0.0.1:18080/

  echo
  printf '%ssmoke test finished%s\n' "$green" "$off"
}

target_clean() {
  rm -f "$BINARY"
  rm -rf dist web/.next web/out
  mkdir -p web/out && touch web/out/.gitkeep
  say "cleaned"
}

need go "Install Go 1.25 or newer from https://go.dev/dl/"

case "${1:-all}" in
  all)   target_web; target_vet; target_test; target_build; target_examples ;;
  web)   target_web ;;
  quick) target_build ;;
  vet)   target_vet ;;
  test)  target_test ;;
  dev)   target_dev ;;
  cross) target_cross ;;
  examples) target_examples ;;
  run)   target_run ;;
  smoke) target_smoke ;;
  clean) target_clean ;;
  *)     die "unknown target '$1'. Valid: all, web, quick, vet, test, dev, examples, cross, run, smoke, clean" ;;
esac
