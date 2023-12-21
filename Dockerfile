FROM golang:1.21

RUN apt-get update && apt-get install -y --no-install-recommends \
                openssh-client \
                rsync \
                fuse3 \
                sshfs \
        && rm -rf /var/lib/apt/lists/*

RUN GO111MODULE=on go install golang.org/x/lint/golint@latest \
						golang.org/go/x/tools/...@latest

ENV USER root
WORKDIR /go/src/github.com/docker/machine
RUN go mod init

COPY . ./
RUN mkdir bin
