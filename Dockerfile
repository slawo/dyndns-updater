FROM gcr.io/distroless/static-debian13:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99
ARG TARGETPLATFORM
COPY --chmod=755 .bin/${TARGETPLATFORM}/dyndns-updater /usr/local/bin/dyndns-updater
COPY examples/urls.txt /etc/dyndns-updater/urls.txt
COPY --chown=65532:65532 deploy/state/ /var/lib/dyndns-updater/
ENV DYNDNS_LOOKUP_FILE=/etc/dyndns-updater/urls.txt \
    DYNDNS_STATE_FILE=/var/lib/dyndns-updater/last-ip
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/dyndns-updater"]
