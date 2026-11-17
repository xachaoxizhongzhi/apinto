FROM alpine:3.22
RUN apk add --no-cache bash ca-certificates tzdata netcat-openbsd curl
ENV TZ=Asia/Shanghai
COPY apinto/ /apinto/
RUN mkdir -p /etc/apinto \
    && cp /apinto/apinto.yml.tpl /etc/apinto/apinto.yml \
    && cp /apinto/config.yml.tpl /etc/apinto/config.yml
VOLUME /var/lib/apinto
WORKDIR /apinto
EXPOSE 8099 9400 9401
CMD ["bash", "/apinto/auto-start.sh"]
