FROM alpine:3.13.4

WORKDIR /app
COPY ./bin/awsstatecheck_linux_amd64 /app/awsstatecheck
RUN chmod +x /app/awsstatecheck

ENTRYPOINT /app/awsstatecheck $COMMAND