FROM node:22-alpine AS web-build

WORKDIR /src/apps/web
COPY apps/web/package.json apps/web/package-lock.json ./
RUN npm ci
COPY apps/web/ ./
RUN npm run build

FROM golang:1.24-alpine AS api-build

WORKDIR /src
COPY services/api/go.mod services/api/go.sum ./services/api/
RUN cd services/api && go mod download
COPY services/api/ ./services/api/
RUN cd services/api && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/inferlab-api .

FROM alpine:3.21

RUN apk add --no-cache ca-certificates wget \
  && addgroup -S inferlab \
  && adduser -S -G inferlab inferlab \
  && mkdir -p /app/data \
  && chown -R inferlab:inferlab /app

WORKDIR /app
COPY --from=api-build /out/inferlab-api /app/inferlab-api
COPY --from=web-build /src/apps/web/dist /app/web

ENV APP_ENV=production \
    LISTEN_ADDR=0.0.0.0:8080 \
    DB_PATH=/app/data/inferlab.db \
    WEB_DIR=/app/web

VOLUME ["/app/data"]
EXPOSE 8080
USER inferlab
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/app/inferlab-api"]
