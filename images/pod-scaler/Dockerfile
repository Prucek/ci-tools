FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

RUN go build ../../cmd/pod-scaler
ADD pod-scaler /usr/bin/pod-scaler
ENTRYPOINT ["/usr/bin/pod-scaler"]
