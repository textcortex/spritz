FROM node:22-alpine AS build

WORKDIR /workspace

RUN corepack enable

COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

COPY . ./
RUN pnpm build

FROM nginxinc/nginx-unprivileged:1.27-alpine

USER root
RUN apk add --no-cache sed

COPY --from=build /workspace/dist/ /usr/share/nginx/html/
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY entrypoint.sh /entrypoint.sh
RUN chown -R 101:101 /usr/share/nginx/html /entrypoint.sh /etc/nginx/conf.d/default.conf

ENV SPRITZ_API_BASE_URL=/api

USER 101:101
EXPOSE 8080
ENTRYPOINT ["/entrypoint.sh"]
