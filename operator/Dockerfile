FROM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod ./
COPY go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/spritz-operator main.go

FROM alpine:3.20
RUN adduser -D -u 65532 spritz
USER 65532:65532
COPY --from=build /out/spritz-operator /usr/local/bin/spritz-operator

ENTRYPOINT ["/usr/local/bin/spritz-operator"]
