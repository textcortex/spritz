# Build with the repository root as context so the shared local Go modules are available.
FROM golang:1.25-alpine AS build

WORKDIR /src
COPY integrations/acptext/go.mod ./integrations/acptext/go.mod
COPY integrations/slack-gateway/go.mod ./integrations/slack-gateway/go.mod
COPY integrations/slack-gateway/go.sum ./integrations/slack-gateway/go.sum
WORKDIR /src/integrations/slack-gateway
RUN go mod download
WORKDIR /src
COPY integrations/acptext/ /src/integrations/acptext/
COPY integrations/slack-gateway/ /src/integrations/slack-gateway/
WORKDIR /src/integrations/slack-gateway
RUN CGO_ENABLED=0 go build -o /out/spritz-slack-gateway .

FROM alpine:3.20
RUN adduser -D -u 65532 spritz
USER 65532:65532
COPY --from=build /out/spritz-slack-gateway /usr/local/bin/spritz-slack-gateway

ENTRYPOINT ["/usr/local/bin/spritz-slack-gateway"]
