FROM golang:1.25-alpine AS build

WORKDIR /src
COPY integrations/github-app/go.mod ./integrations/github-app/go.mod
WORKDIR /src/integrations/github-app
RUN go mod download
COPY integrations/github-app/ /src/integrations/github-app/
RUN CGO_ENABLED=0 go build -o /out/spritz-github-app .

FROM alpine:3.20
RUN adduser -D -u 65532 spritz
USER 65532:65532
COPY --from=build /out/spritz-github-app /usr/local/bin/spritz-github-app

ENTRYPOINT ["/usr/local/bin/spritz-github-app"]
