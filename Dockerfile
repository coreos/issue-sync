FROM alpine:3.6

WORKDIR /opt/issue-sync

RUN apk update --no-cache && apk add ca-certificates

COPY bin/issue-sync /opt/issue-sync/issue-sync

COPY config.json /opt/issue-sync/config.json

ENTRYPOINT ["./issue-sync"]

CMD ["--config", "config.json"]
