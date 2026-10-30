FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /rivoo-agent ./cmd/rivoo-agent
# The config (key, RCON password) lives in /data, owned by the image's non-root user.
RUN mkdir /data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /rivoo-agent /rivoo-agent
COPY --from=build --chown=65532:65532 /data /data
# The image is the version: update by pulling a new tag, not in place.
ENV RIVOO_AUTO_UPDATE=false RIVOO_CONFIG=/data/rivoo-agent.json
VOLUME /data
USER 65532:65532
ENTRYPOINT ["/rivoo-agent"]
CMD ["run"]
