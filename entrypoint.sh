#!/bin/sh
set -e

# If S3_BUCKET is set, mount S3 portfolio/www directly to /storage/persisted/www via s3fs
if [ -n "$S3_BUCKET" ]; then
    mkdir -p /storage/persisted/www

    # Create passwd-s3fs if credentials exist
    if [ -n "$AWS_ACCESS_KEY_ID" ] && [ -n "$AWS_SECRET_ACCESS_KEY" ]; then
        echo "${AWS_ACCESS_KEY_ID}:${AWS_SECRET_ACCESS_KEY}" > /etc/passwd-s3fs
        chmod 600 /etc/passwd-s3fs
    fi

    S3_ENDPOINT_OPT=""
    if [ -n "$S3_ENDPOINT" ]; then
        S3_ENDPOINT_OPT="-o url=${S3_ENDPOINT}"
    fi

    S3_PREFIX_VAL="${S3_PREFIX:-portfolio/}"
    CLEAN_PREFIX=$(echo "$S3_PREFIX_VAL" | sed 's/\/*$//')
    BUCKET_TARGET="${S3_BUCKET}:${CLEAN_PREFIX}/www"

    echo "s3fs: mounting ${BUCKET_TARGET} at /storage/persisted/www..."
    s3fs "${BUCKET_TARGET}" /storage/persisted/www \
        -o use_path_style \
        -o allow_other \
        ${S3_ENDPOINT_OPT} 2>/dev/null || echo "s3fs mount note: mount skipped or managed externally"
fi

exec /portfolio "$@"
