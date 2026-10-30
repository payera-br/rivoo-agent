FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /rivoo-agent ./cmd/rivoo-agent

FROM gcr.io/distroless/static-debian12
COPY --from=build /rivoo-agent /rivoo-agent
# The image is the version: update by pulling a new tag, not in place.
ENV RIVOO_AUTO_UPDATE=false RIVOO_CONFIG=/data/rivoo-agent.json
VOLUME /data
ENTRYPOINT ["/rivoo-agent"]
CMD ["run"]
