# My first app

A small web page that customers deploy through GLBNXT **My Apps** to see how it
works. It celebrates with confetti, shows the settings they entered, writes a
log line on request, tests the platform services, and looks different between
versions so an update is visible.

## Deploy it in My Apps

| Field | Value |
| --- | --- |
| Application name | `hello` (address: `hello.<organisation>.glbnxt.com`) |
| Image repository | `ghcr.io/glbnxt/my-first-app` |
| Tag | `1.0.0` (then update to `1.1.0` and `1.2.0`) |
| Container port | `8080` |
| Logo | `https://raw.githubusercontent.com/GLBNXT/my-first-app/main/logo.png` |

## Settings

| Variable | Effect | Options | Default |
| --- | --- | --- | --- |
| `PAGE_COLOR` | Page colour | blue, green, purple, orange, pink, red, teal, or `#rrggbb` | blue in 1.0.0, green in 1.1.0, purple in 1.2.0 |
| `CONFETTI_STYLE` | Confetti when the page opens | classic, stars, snow, hearts, party, off | classic |
| `GREETING` | Line under the heading | any text (max 120 characters) | "Welcome to your first application." |
| `PORT` | Port to listen on | | 8080 |

A value the app does not recognise falls back to the default; the page and a
`WARN` log line say so.

## Platform services (1.2.0)

Switch a service on in My Apps, under Platform services. The platform then
restarts the app with the settings below. The **Platform services** page shows
each service as connected or not, and a **Test connection** button proves it
works. A service whose settings are missing reads "Not connected".

| Service | Settings the platform sets | What the test does |
| --- | --- | --- |
| Model Hub | `GLBNXT_MODELHUB_BASE_URL`, `GLBNXT_MODELHUB_API_KEY` (also `OPENAI_BASE_URL` and `OPENAI_API_KEY`, unless you set those yourself) | `GET <base URL>/models` with the key; shows the models the app may use |
| Database | `GLBNXT_DATABASE_HOST`, `_PORT`, `_NAME`, `_USER`, `_PASSWORD` | Connects over TLS (`sslmode=require`), creates the table `visits` if needed, writes a visit and reads the latest five back |

Keys and passwords never reach the page: it only gets the setting names, the
service address and the test result. When a test fails, the page shows a plain
sentence and the app writes the detail to its log as a `WARN` line.

The database connection is encrypted, but the app does not check the server's
certificate, because the platform gives it none to check against.

## What it does

- `GET /` serves the page (embedded in the binary, no external requests).
- `GET /api/info` returns version, address, settings and which platform
  services are connected.
- `POST /api/services/modelhub/test` and `POST /api/services/database/test`
  run the tests above. Rate-limited together, 10 seconds max per test.
- `POST /api/log` writes `{"msg":"Message from the demo page","message":"..."}`
  to stdout. Rate-limited, 200 characters max.
- `GET /healthz` returns 204.
- On `SIGTERM` it finishes open requests before stopping.

The image is built `FROM scratch` with root certificates added, about 15 MB, and
runs as numeric user `10001`, which My Apps requires. The only dependency is
the Postgres driver [pgx](https://github.com/jackc/pgx) (MIT).

## Release

Versions differ only in the `releases` map in `main.go` (default colour and
notes); the version comes from the tag at build time.

```bash
git tag v1.2.0 && git push origin v1.2.0
```

The [publish workflow](.github/workflows/publish.yml) builds for amd64 and
arm64 and pushes `ghcr.io/glbnxt/my-first-app:<version>`. Make the package
public once, in the package settings on GitHub.

## Run locally

```bash
docker build --build-arg VERSION=1.0.0 -t my-first-app:1.0.0 .
docker run --rm -p 8080:8080 -e PAGE_COLOR=purple my-first-app:1.0.0
```

To try the database test locally, start a Postgres with TLS and pass the five
`GLBNXT_DATABASE_*` settings with `-e`.

Open <http://hello.acme.localtest.me:8080> to see the application and
organisation names filled in (they are read from the address).

Confetti: [canvas-confetti](https://github.com/catdad/canvas-confetti) 1.9.4,
vendored in `web/` (ISC licence, `web/confetti.LICENSE`).

Look and feel: the GLBNXT portal tokens and component sizes from `@glbnxt/ui`
(shadcn/ui) in plain CSS. Fonts vendored in `web/fonts/`: Inter (OFL, from
`@fontsource/inter` 5.3.0), Archivo SemiBold and Red Hat Mono (OFL, from the
GLBNXT CDN). Icons: [Lucide](https://lucide.dev) (ISC), rendered into
`web/icons.js`.
