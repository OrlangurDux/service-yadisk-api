FROM golang:1.23.4-alpine AS build

ARG VERSION=0.0.1
ARG BIN_NAME=yadisk-api

WORKDIR /src
COPY ./src .
RUN apk --no-cache add gcc musl-dev upx \
    && go mod vendor \
    && CGO_ENABLED=0 go build -ldflags="-X 'main.Version=${VERSION}'" -o "${BIN_NAME}" ./ && upx --best --lzma /src/yadisk-api

FROM scratch
COPY --from=build /src/yadisk-api /

ENTRYPOINT [ "/yadisk-api" ]

EXPOSE 9277
