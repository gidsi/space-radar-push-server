FROM golang:1.12-alpine as builder
RUN apk --no-cache add git
WORKDIR /go/src/app
COPY main.go main.go
RUN go get -d  ./...
RUN go generate
RUN go install ./...


FROM alpine:latest
RUN apk update && apk add ca-certificates && rm -rf /var/cache/apk/*
COPY --from=builder /go/bin/app .
EXPOSE 8080
RUN adduser app -S -u 142
USER app

CMD ["./app"]
