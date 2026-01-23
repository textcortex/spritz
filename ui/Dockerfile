FROM nginxinc/nginx-unprivileged:1.27-alpine

USER root
RUN apk add --no-cache sed

COPY public/ /usr/share/nginx/html/
COPY entrypoint.sh /entrypoint.sh
RUN chown -R 101:101 /usr/share/nginx/html /entrypoint.sh

ENV SPRITZ_API_BASE_URL=/api

USER 101:101
EXPOSE 8080
ENTRYPOINT ["/entrypoint.sh"]
