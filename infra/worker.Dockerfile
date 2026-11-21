FROM golang:1.24-alpine AS build
WORKDIR /src
COPY services/api/go.mod services/api/go.sum ./
RUN go mod download
COPY services/api/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/inferlab .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates iptables util-linux
COPY --from=build /out/inferlab /app/inferlab
COPY infra/worker-isolation.sh /app/worker-isolation.sh
ENTRYPOINT ["/app/worker-isolation.sh"]
