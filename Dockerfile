# ---- Stage 1: build the React dashboard ----
FROM node:24-alpine AS frontend
WORKDIR /web
COPY internal/web/package.json internal/web/package-lock.json ./
RUN npm ci
COPY internal/web/ ./
RUN npm run build

# ---- Stage 2: build the Go binary with embedded dashboard ----
FROM golang:1.25-alpine AS backend
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Copy the built dashboard into the embed source path
COPY --from=frontend /web/dist ./internal/web/dist
RUN CGO_ENABLED=0 go build -tags embed -ldflags="-s -w" -o /gorouter ./cmd/gorouter

# ---- Stage 3: minimal runtime ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=backend /gorouter /usr/local/bin/gorouter
COPY docker-entrypoint.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/docker-entrypoint.sh
ENV GOROUTER_PORT=20128
EXPOSE 20128
ENTRYPOINT ["docker-entrypoint.sh"]