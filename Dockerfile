FROM golang:1.27.3-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod ./
COPY main.go ./
COPY web ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /my-first-app .

FROM scratch
COPY --from=build /my-first-app /my-first-app
# A numeric user: My Apps only starts images that run as a non-root user it
# can verify, and it can only verify a number.
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/my-first-app"]
