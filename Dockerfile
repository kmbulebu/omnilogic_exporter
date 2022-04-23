FROM quay.io/prometheus/busybox:latest

ARG TARGETARCH
ARG TARGETOS
COPY ./bin/omnilogic_exporter_${TARGETOS}_${TARGETARCH} /bin/omnilogic_exporter

EXPOSE      9190
USER        nobody
ENTRYPOINT  [ "/bin/omnilogic_exporter" ]
