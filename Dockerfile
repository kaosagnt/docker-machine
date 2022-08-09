FROM golang:1.15

RUN apt-get update && apt-get install -y --no-install-recommends \
                openssh-client \
                rsync \
                fuse \
                sshfs \
        && rm -rf /var/lib/apt/lists/*

RUN GO111MODULE=on go get golang.org/x/lint/golint \
                          golang.org/x/tools/cover

ENV USER root
WORKDIR /go/src/github.com/docker/machine

COPY . ./
RUN mkdir bin
