# syntax=docker/dockerfile:1.7

FROM mcr.microsoft.com/playwright:v1.57.0 AS runtime-base

WORKDIR /app

CMD ["/bin/bash"]
ENTRYPOINT ["/app/follower"]


FROM runtime-base AS final
ARG LOCAL_BINARY=bin/follower-linux-amd64
ARG LOCAL_CTL_BINARY=bin/followerctl-linux-amd64

COPY bin/playwright-driver-1.57.0 /opt/playwright-driver
COPY bin/dlv /usr/bin/dlv

RUN chmod +x /opt/playwright-driver/node /opt/playwright-driver/package/*.sh 2>/dev/null || true
COPY --chmod=0755 ${LOCAL_BINARY} /app/follower
COPY --chmod=0755 ${LOCAL_CTL_BINARY} /app/followerctl
