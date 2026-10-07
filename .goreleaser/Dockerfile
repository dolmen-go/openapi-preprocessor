# Used by GoReleaser only (see dockers_v2 in ../.goreleaser.yaml): it copies a
# binary prebuilt for $TARGETPLATFORM and cannot build from a source checkout.
FROM scratch
ARG TARGETPLATFORM
COPY --chown=0:0 --chmod=0555 $TARGETPLATFORM/openapi-preprocessor /usr/local/bin/openapi-preprocessor
WORKDIR /work
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/openapi-preprocessor"]
