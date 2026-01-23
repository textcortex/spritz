FROM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod ./go.mod
COPY main.go ./main.go
RUN CGO_ENABLED=0 go build -o /out/spritz-gateway .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
RUN adduser -D -u 65532 spritz
USER 65532:65532
COPY --from=build /out/spritz-gateway /usr/local/bin/spritz-gateway

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/spritz-gateway"]
