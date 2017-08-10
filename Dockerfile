FROM alpine:3.6

WORKDIR /opt/issue-sync

ADD bin/issue-sync /opt/issue-sync/issue-sync

CMD ["./issue-sync"]