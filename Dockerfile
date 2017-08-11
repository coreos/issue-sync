FROM alpine:3.6

WORKDIR /opt/issue-sync

COPY bin/issue-sync /opt/issue-sync/issue-sync

ENTRYPOINT ./issue-sync
