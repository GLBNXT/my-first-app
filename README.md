# My first app

A small web page that customers deploy through GLBNXT **My Apps** to see how it
works. It celebrates with confetti, shows the settings they entered, writes a
log line on request, and looks different between versions so an update is
visible.

## Deploy it in My Apps

| Field | Value |
| --- | --- |
| Application name | `hello` (address: `hello.<organisation>.glbnxt.com`) |
| Image repository | `ghcr.io/glbnxt/my-first-app` |
| Tag | `1.0.0` (then update to `1.1.0`) |
| Container port | `8080` |

## Settings

| Variable | Effect | Options | Default |
| --- | --- | --- | --- |
| `PAGE_COLOR` | Page colour | blue, green, purple, orange, pink, red, teal, or `#rrggbb` | blue in 1.0.0, green in 1.1.0 |
| `CONFETTI_STYLE` | Confetti when the page opens | classic, stars, snow, hearts, party, off | classic |
| `GREETING` | Line under the heading | any text (max 120 characters) | "Welcome to your first application." |
| `PORT` | Port to listen on | | 8080 |

A value the app does not recognise falls back to the default; the page and a
`WARN` log line say so.

## What it does

- `GET /` serves the page (embedded in the binary, no external requests).
- `GET /api/info` returns version, address, settings.
- `POST /api/log` writes `{"msg":"Message from the demo page","message":"..."}`
  to stdout. Rate-limited, 200 characters max.
- `GET /healthz` returns 204.
- On `SIGTERM` it finishes open requests before stopping.

The image is built `FROM scratch`, about 10 MB, and runs as numeric user
`10001`, which My Apps requires.

## Release

Versions differ only in the `releases` map in `main.go` (default colour and
notes); the version comes from the tag at build time.

```bash
git tag v1.0.0 && git tag v1.1.0 && git push origin v1.0.0 v1.1.0
```

The [publish workflow](.github/workflows/publish.yml) builds for amd64 and
arm64 and pushes `ghcr.io/glbnxt/my-first-app:<version>`. Make the package
public once, in the package settings on GitHub.

## Run locally

```bash
docker build --build-arg VERSION=1.0.0 -t my-first-app:1.0.0 .
docker run --rm -p 8080:8080 -e PAGE_COLOR=purple my-first-app:1.0.0
```

Open <http://hello.acme.localtest.me:8080> to see the application and
organisation names filled in (they are read from the address).

Confetti: [canvas-confetti](https://github.com/catdad/canvas-confetti) 1.9.4,
vendored in `web/` (ISC licence, `web/confetti.LICENSE`).

Look and feel: the GLBNXT portal tokens and component sizes from `@glbnxt/ui`
(shadcn/ui) in plain CSS. Fonts vendored in `web/fonts/`: Inter (OFL, from
`@fontsource/inter` 5.3.0), Archivo SemiBold and Red Hat Mono (OFL, from the
GLBNXT CDN). Icons: [Lucide](https://lucide.dev) (ISC), rendered into
`web/icons.js`.
