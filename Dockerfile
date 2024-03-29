FROM golang:1.22.1-alpine3.19 as builder

RUN apk update && apk add --no-cache make git

WORKDIR /

COPY . .

RUN make build-linux

FROM alpine:3.19.1

COPY --from=builder /bin/object-storage-ui_linux-amd64 /bin

ENTRYPOINT [ "/bin/object-storage-ui_linux-amd64" ]