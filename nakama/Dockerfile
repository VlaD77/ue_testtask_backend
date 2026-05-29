# syntax=docker/dockerfile:1

ARG NAKAMA_VERSION=3.31.0

FROM --platform=linux/amd64 heroiclabs/nakama-pluginbuilder:${NAKAMA_VERSION} AS builder

ENV GO111MODULE=on
ENV CGO_ENABLED=1

WORKDIR /backend
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN go build --trimpath --buildmode=plugin -o encounter.so .

FROM --platform=linux/amd64 heroiclabs/nakama:${NAKAMA_VERSION}

COPY --from=builder /backend/encounter.so /nakama/data/modules/encounter.so

EXPOSE 7349 7350 7351
