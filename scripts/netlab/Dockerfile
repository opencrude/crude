# One image for every network-lab role: client, router, server and provider.
# BASE is the verified, imported official Ubuntu base tarball (see netlab.py);
# no registry pull is involved. Host keys are removed so each container
# generates its own at provisioning; no credential is baked into the image.
ARG BASE
FROM ${BASE}
ENV DEBIAN_FRONTEND=noninteractive LANG=C.UTF-8 LC_ALL=C.UTF-8
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      openssh-server openssh-client tmux iproute2 iptables conntrack ethtool \
      iputils-ping tcpdump python3 procps git ca-certificates \
 && rm -rf /var/lib/apt/lists/* /etc/ssh/ssh_host_* \
 && useradd --create-home --uid 1000 --shell /bin/bash crude \
 && mkdir -p /run/sshd \
 && ln -s /opt/crude/crude /usr/local/bin/crude
