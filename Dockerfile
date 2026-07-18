# syntax=docker/dockerfile:1

# Production Dockerfile for goreleaser (dockers_v2).
# The kadence binary is pre-built by goreleaser with the frontend embedded
# via //go:embed, so no separate frontend build stage is needed here.

FROM gcr.io/distroless/static-debian12:nonroot

ARG TARGETPLATFORM

LABEL org.opencontainers.image.source="https://github.com/tamcore/kadence"
LABEL org.opencontainers.image.description="Kadence - self-hostable AI coach"
LABEL org.opencontainers.image.licenses="MIT"

COPY ${TARGETPLATFORM}/kadence /kadence

EXPOSE 8080

USER 65532:65532

ENTRYPOINT ["/kadence"]
