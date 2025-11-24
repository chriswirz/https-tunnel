# Docker examples

Each directory is a self-contained `compose.yaml` using an inline Dockerfile, so
there is nothing to copy but the one file.

| | |
| --- | --- |
| [`expose-a-service/`](expose-a-service/compose.yaml) | the **client** side: publish a containerised HTTP service on a public URL, with nothing listening on the host |
| [`tunnel-server/`](tunnel-server/compose.yaml) | the **server** side: the tunnel server sharing a container with the nginx that fronts it |

Both need Compose v2.17 or newer for `dockerfile_inline`, and
`expose-a-service` needs v2.23 or newer for `configs.content`. On anything
older, move the Dockerfile and the index page into files next to the compose
file.

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
