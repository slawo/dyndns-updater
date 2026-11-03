FROM gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329
ARG TARGETPLATFORM
COPY --chmod=755 .bin/${TARGETPLATFORM}/dyndns-updater /usr/local/bin/dyndns-updater
COPY examples/urls.txt /etc/dyndns-updater/urls.txt
COPY --chown=65532:65532 deploy/state/ /var/lib/dyndns-updater/
ENV DYNDNS_LOOKUP_FILE=/etc/dyndns-updater/urls.txt \
    DYNDNS_STATE_FILE=/var/lib/dyndns-updater/last-ip
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/dyndns-updater"]
