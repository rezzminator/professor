# The journey machine: Ubuntu 24.04 booting systemd as PID 1, a lingering owner (uid 1000, zsh) whose
# user manager runs pfm's services, tmux, git, the Go toolchain at pfm/go.mod's version, Herdr from
# infra/fence/pinned-tools.sh. It holds nothing of pfm/: a pfm edit never rebuilds it.
# infra/fence/journey/run.sh builds it from a context holding this file and pinned-tools.sh, and tags
# it with a hash of those inputs.
FROM ubuntu:24.04
ARG GO_VERSION
ARG GO_ARCH
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
    systemd systemd-sysv dbus dbus-user-session libpam-systemd \
    ca-certificates curl git jq make zsh tmux procps sqlite3 python3 \
 && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz" | tar -C /usr/local -xz \
 && /usr/local/go/bin/go version
COPY pinned-tools.sh /tmp/pinned-tools.sh
RUN bash /tmp/pinned-tools.sh /usr/local herdr && rm /tmp/pinned-tools.sh
# Ubuntu's image user holds uid 1000; the owner lingers so the user manager runs from boot.
# The Go caches exist owned by the owner, so a fresh cache volume mounted there is the owner's.
RUN userdel -r ubuntu \
 && useradd -m -u 1000 -s /usr/bin/zsh tester \
 && mkdir -p /var/lib/systemd/linger && touch /var/lib/systemd/linger/tester \
 && install -d -o tester -g tester /home/tester/go /home/tester/go/pkg /home/tester/go/pkg/mod \
      /home/tester/.cache /home/tester/.cache/go-build \
 && systemctl mask systemd-networkd-wait-online.service getty@tty1.service console-getty.service
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
