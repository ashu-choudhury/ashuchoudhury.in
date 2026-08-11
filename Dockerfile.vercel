# Build stage: compile the single binary.
FROM golang:1.24-alpine AS build

WORKDIR /src
ENV GOTOOLCHAIN=auto

# Cache dependencies first (only re-runs on go.mod/go.sum changes).
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source and build.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/portfolio .

RUN mkdir -p /out/storage/persisted

# ---------------------------------------------------------------------------
# Runtime stage: Alpine base with s3fs & fuse for direct S3 www folder mounting.
FROM alpine:latest

RUN apk add --no-cache s3fs fuse ca-certificates tzdata

COPY --from=build /out/portfolio /portfolio
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

VOLUME ["/storage"]

ENV PORT=8080 \
    DB_PATH=/storage/persisted/portfolio.db \
    SITE_URL=https://ashuchoudhury.in

EXPOSE 8080
ENTRYPOINT ["/entrypoint.sh"]
