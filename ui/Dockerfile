FROM node:22-alpine AS build

WORKDIR /workspace/ui

RUN corepack enable

COPY ui/package.json ui/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

COPY ui/ ./
RUN pnpm build

FROM nginxinc/nginx-unprivileged:1.27-alpine

USER root
RUN apk add --no-cache sed

COPY --from=build /workspace/ui/dist/ /usr/share/nginx/html/
COPY ui/nginx.conf /etc/nginx/conf.d/default.conf
COPY ui/entrypoint.sh /entrypoint.sh
RUN chown -R 101:101 /usr/share/nginx/html /entrypoint.sh /etc/nginx/conf.d/default.conf

ENV SPRITZ_API_BASE_URL=/api

USER 101:101
EXPOSE 8080
ENTRYPOINT ["/entrypoint.sh"]
