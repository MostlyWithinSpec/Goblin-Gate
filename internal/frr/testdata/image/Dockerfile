# FRR as Debian 12 ships it, for the dynamic routing e2e (frr_e2e_test.go).
FROM debian:12
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends frr frr-pythontools iproute2 procps && rm -rf /var/lib/apt/lists/*
CMD ["sleep", "infinity"]
