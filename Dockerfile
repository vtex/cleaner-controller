# Build the manager binary
FROM golang:1.19 as builder
ARG TARGETOS
ARG TARGETARCH

ARG VERSION=none
ARG CA_TOKEN=none

RUN echo "${VERSION}"
RUN echo "${CA_TOKEN}"

