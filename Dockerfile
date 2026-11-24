FROM golang:1.23.4-alpine AS build

ARG VERSION=0.0.1
ARG BIN_NAME=yadisk-api

WORKDIR /src
COPY ./src/go.mod ./src/go.sum ./
RUN go mod download
COPY ./src .
RUN apk --no-cache add gcc musl-dev upx \
    && CGO_ENABLED=0 go build -ldflags="-s -w -X 'main.Version=${VERSION}'" -o "${BIN_NAME}" ./ \
    && upx --best --lzma "/src/${BIN_NAME}"

FROM scratch
# CA-сертификаты обязательны: без них HTTPS-запросы к cloud-api.yandex.net
# падают с x509-ошибкой; tzdata нужна для корректного времени в логах
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build /src/yadisk-api /

EXPOSE 9277
ENTRYPOINT [ "/yadisk-api" ]
