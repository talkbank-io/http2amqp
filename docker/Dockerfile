FROM golang:1.12.6-alpine3.10

COPY http2amqp.go /go/http2amqp.go
COPY docker/entrypoint /usr/local/bin/entrypoint

RUN chmod a+x /usr/local/bin/entrypoint

RUN apk add git && go get github.com/streadway/amqp && go build http2amqp.go

# ENTRYPOINT [ "/bin/sh" ]

ENTRYPOINT [ "/usr/local/bin/entrypoint" ]