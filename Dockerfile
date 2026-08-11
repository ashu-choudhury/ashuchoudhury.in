# Build stage: compile the single binary.
# Uses a pinned Go version for reproducible builds.
FROM golang:1.24-alpine AS build

WORKDIR /src
ENV GOTOOLCHAIN=auto

# Cache dependencies first (only re-runs on go.mod/go.sum changes).
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source and build.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/portfolio .

# Pre-create the data directory for SQLite database persistence.
RUN mkdir -p /out/storage/persisted && chown -R 10001:10001 /out/storage

# ---------------------------------------------------------------------------
# Runtime stage: scratch keeps the image tiny (~15 MB) and single-purpose.
FROM scratch

# Trusted CA bundle so S3 / outbound API calls work.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# The compiled binary, owned by the runtime user.
COPY --from=build --chown=10001:10001 /out/portfolio /portfolio

# The data directory, owned by the runtime user (for SQLite DB).
COPY --from=build --chown=10001:10001 /out/storage /storage

# Only the SQLite database file lives on disk.
VOLUME ["/storage"]

# Non-root for security: uid/gid 10001 (like distroless).
USER 10001:10001

ENV PORT=8080 \
    DB_PATH=/storage/persisted/portfolio.db \
    SITE_URL=https://ashuchoudhury.in

EXPOSE 8080
ENTRYPOINT ["/portfolio"]
