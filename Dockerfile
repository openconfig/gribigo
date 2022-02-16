FROM golang:1

WORKDIR /go/src/app
COPY . .

RUN go get -d -v ./... && go install -v ./...

ENTRYPOINT ["/go/bin/rtr"]
