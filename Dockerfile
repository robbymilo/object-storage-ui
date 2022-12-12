FROM golang:1.19.4-alpine3.17 as builder

RUN apk update && apk add make git && rm -rf /var/cache/apk/*

WORKDIR /

COPY . .

RUN make build-linux

ARG SHA
ENV SHA=$SHA

ENTRYPOINT [ "/bin/object-storage-ui" ]