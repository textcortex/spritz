FROM golang:1.25-alpine AS build

WORKDIR /src
COPY api/go.mod api/go.sum ./api/
COPY operator/go.mod operator/go.sum ./operator/
WORKDIR /src/api
RUN go mod download
COPY api/ /src/api/
COPY operator/ /src/operator/
RUN CGO_ENABLED=0 go build -o /out/spritz-api .
RUN CGO_ENABLED=0 go build -o /out/spritz-shared-syncer ./cmd/shared-syncer

FROM alpine:3.20
RUN apk add --no-cache ca-certificates rclone \
  && adduser -D -u 65532 spritz
USER 65532:65532
COPY --from=build /out/spritz-api /usr/local/bin/spritz-api
COPY --from=build /out/spritz-shared-syncer /usr/local/bin/spritz-shared-syncer

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/spritz-api"]
