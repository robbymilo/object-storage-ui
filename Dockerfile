FROM golang:1.19.4-alpine3.17 as builder

RUN apk update && apk add make git && rm -rf /var/cache/apk/*

WORKDIR /

COPY . .

RUN make build-linux

FROM alpine:3.17

ARG SHA
ENV SHA=$SHA

COPY --from=builder /bin/object-storage-ui-${SHA}_linux-amd64 /bin/object-storage-ui

ENTRYPOINT [ "/bin/object-storage-ui" ]