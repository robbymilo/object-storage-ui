FROM golang:1.19.4-alpine3.17

RUN apk update && apk add make && rm -rf /var/cache/apk/*