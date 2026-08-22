FROM alpine:3.24.1
COPY ./client /usr/local/bin/
RUN chown -R nobody:nogroup /usr/local/bin/client
USER nobody
ENTRYPOINT [ "client" ]
