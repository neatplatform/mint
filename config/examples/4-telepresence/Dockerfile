FROM alpine:3.24.1
EXPOSE 8080
COPY ./server /usr/local/bin/
RUN chown -R nobody:nogroup /usr/local/bin/server
USER nobody
ENTRYPOINT [ "server" ]
