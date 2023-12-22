FROM golang:1.21

RUN apt-get update && apt-get install -y --no-install-recommends \
                openssh-client \
                rsync \
                fuse3 \
                sshfs \
        && rm -rf /var/lib/apt/lists/*

ENV GO111MODULE=on

ENV USER root
WORKDIR /go/src/github.com/docker/machine

COPY . ./
RUN mkdir bin
