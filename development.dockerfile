FROM golang:1.23.4-alpine

ENV GO111MODULE=on
WORKDIR /app
COPY ./src/go.mod ./src/go.sum ./

RUN go mod download
RUN go install github.com/githubnemo/CompileDaemon@v1.4.0
COPY ./src /app
ENTRYPOINT CompileDaemon --build="go build -o main" --command=./main
