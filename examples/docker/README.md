# Docker examples

Each directory is a self-contained `compose.yaml` using an inline Dockerfile, so
there is nothing to copy but the one file.

| | |
| --- | --- |
| [`expose-a-service/`](expose-a-service/compose.yaml) | the **client** side: publish a containerised HTTP service on a public URL, with nothing listening on the host |
| [`tunnel-server/`](tunnel-server/compose.yaml) | the **server** side: the tunnel server sharing a container with the nginx that fronts it, on `nginx:1.27-alpine` |
| [`tunnel-server-debian/`](tunnel-server-debian/compose.yaml) | the same stack on `nginx:latest`, which is Debian, for anyone who wants glibc or apt |

All of them need Compose v2.17 or newer for `dockerfile_inline`, and
`expose-a-service` needs v2.23 or newer for `configs.content`. On anything
older, move the Dockerfile and the index page into files next to the compose
file.

## If the entrypoint fails to parse

```
/entrypoint.sh: 23: Syntax error: "fi" unexpected (expecting "then")
```

means the compose file was checked out with CRLF line endings. Git does that on
Windows when `core.autocrlf=true` and nothing says otherwise, the carriage
returns travel through the inline Dockerfile into the script, and the shell
reads `then
`, which is not `then`; it then complains at the next structural
token, the `fi`. The `.gitattributes` at the root of this repository pins these
files to LF, and each image strips trailing whitespace from the entrypoint as a
second line of defense, so a copy that picked up CRLF elsewhere still runs.

To see what a built image actually holds:

```bash
docker compose run --rm --entrypoint cat tunnel /entrypoint.sh | cat -A | head
```

`$` marks each line end. `^M$` means carriage returns are still there.

## Which build gets downloaded

Each image pulls a release binary through the `TUNNEL_RELEASE` build argument,
which defaults to `latest`. The workflow keeps "latest" pointing at the newest
commit on the default branch, so a rebuild picks up the current code:

```bash
docker compose build --no-cache      # newest commit
TUNNEL_RELEASE=v1.2.3 docker compose build --no-cache   # a fixed version
```

The Dockerfile switches URL shape on that value, because GitHub spells the two
differently: `/releases/latest/download/<asset>` against
`/releases/download/<tag>/<asset>`. It writes both URLs out in full rather than
assembling them from shell variables, because **Docker substitutes every `$var`
on a `RUN` line before the shell runs**, and a name that is not a build argument
becomes an empty string. A `base="https://..."` helper variable would arrive as
`""` and curl would be handed an empty URL. Only `TUNNEL_RELEASE` appears there,
and it is a build argument.

The same applies **inside the heredoc that writes the entrypoint**. A quoted
delimiter (`<<'SH'`) stops the *shell* expanding the body, but Docker has
already been through it by then, so `$CONFIG` and `$TUNNEL_API_KEY` would be
replaced with empty strings and baked into the file. The symptom is a script
that fails at run time with

```
/entrypoint.sh: 9: cannot create : Directory nonexistent
```

with nothing between "create" and the colon, because the filename it was given
is an empty string rather than a missing path. Every dollar that has to survive
to run time is therefore written `\$` in the compose file: Docker turns it into
a plain `$`, the shell writes that verbatim, and the container reads its
environment when it starts.

An image that must rebuild to the same bytes should pin a version tag. `latest`
moves with every merge, which is convenient and is not reproducible.

To verify the download inside the image, fetch the `.sha256` published beside
each binary and check it before `chmod +x`.

## expose-a-service

```bash
cd expose-a-service
echo 'TUNNEL_API_KEY=the-key-your-admin-gave-you' > .env
docker compose up -d
docker compose logs tunnel     # prints the public URL
```

Four things in there are worth copying into a stack of your own:

- **`local_host` is the service name**, not `127.0.0.1`. The client and the
  thing it exposes are different containers, so loopback would point at the
  client container itself.
- **The config lives on a volume**, because the client writes the issued session
  id back into it. Without that, every restart gets a new URL. The entrypoint
  only writes the file when it is absent, so the id survives.
- **The API key comes from the environment**, and `${TUNNEL_API_KEY:?...}` makes
  compose refuse to start rather than silently building a config with an empty
  key.
- **No `ports:` anywhere.** The app is reachable through the tunnel and nowhere
  else, which is the point.

To serve a folder rather than a service, drop the `app` service and swap the two
`local_*` lines for `"local_dir": "/srv"` with the folder mounted there.

## tunnel-server

```bash
cd tunnel-server
cp ../../../nginx/tunnel.example.com.conf ./site.conf
sed -i 's/tunnel\.example\.com/your.domain.here/g' site.conf

printf 'TUNNEL_BASE_DOMAIN=your.domain.here\nTUNNEL_API_KEY=%s\n' "$(openssl rand -hex 24)" > .env
docker compose up -d
```

Then open `https://your.domain.here/` and sign in with **admin / admin**, which
the UI immediately makes you replace.

nginx and the tunnel server share one container on purpose. They share a network
namespace, so the server binds `127.0.0.1:8080` and is unreachable from anywhere
but nginx, which is exactly what the `upstream` in the shipped site file
expects. Splitting them into two services would mean exposing that port on the
compose network and pointing the upstream at a service name instead.

The entrypoint starts both and watches them: if either exits, it stops the other
and exits non-zero, so `restart: unless-stopped` brings up a clean pair rather
than leaving a container running with half of itself dead.

What stays outside the container:

- **DNS**, an `A`/`AAAA` for the apex and another for `*.your.domain.here`.
- **Certificates.** `/etc/letsencrypt` is mounted read-only; renew on the host
  with certbot's DNS-01 challenge, which is the only one that issues a wildcard.
  `docker compose restart tunnel` picks up a renewed certificate.

The generated `config.json` carries a single API key. Mount your own file over
`/data/config.json` when you need several, or edit the one on the volume:

```bash
docker compose exec tunnel vi /data/config.json && docker compose restart tunnel
```

## tunnel-server-debian

The same stack built on `nginx:latest`, which is Debian rather than Alpine. It
exists because a Dockerfile written for one base does not simply run on the
other:

| | alpine | debian |
| --- | --- | --- |
| packages | `apk add --no-cache ca-certificates curl` | `apt-get update && apt-get install -y --no-install-recommends ...`, then `rm -rf /var/lib/apt/lists/*` |
| `/bin/sh` | busybox ash | dash |
| libc | musl | glibc |
| image size | roughly 50 MB | roughly 190 MB |

The entrypoint is byte for byte the same, because it sticks to POSIX shell;
anything bash flavored would have needed rewriting. The published binary is
built with `CGO_ENABLED=0` and is static, so it does not care which libc is
there.

Pick alpine unless something else in your stack wants glibc or a Debian package.
Either way, pin the tag: `nginx:latest` moves under you, and a new nginx major
is not a change to discover from a restart.
