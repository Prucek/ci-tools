FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

RUN go build ../../cmd/ci-secret-bootstrap
ADD ci-secret-bootstrap /usr/bin/ci-secret-bootstrap
ENTRYPOINT ["/usr/bin/ci-secret-bootstrap"]
