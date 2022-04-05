ARG ARCH="amd64"
ARG OS="linux"
FROM quay.io/prometheus/busybox-${OS}-${ARCH}:latest
LABEL maintainer="The Prometheus Authors <prometheus-developers@googlegroups.com>"

ARG ARCH="amd64"
ARG OS="linux"
COPY .build/${OS}-${ARCH}/omnilogic_exporter /bin/omnilogic_exporter

EXPOSE      9101
USER        nobody
ENTRYPOINT  [ "/bin/omnilogic_exporter" ]
