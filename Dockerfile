FROM gcr.io/distroless/static

ARG TARGETOS TARGETARCH

COPY nanohub-$TARGETOS-$TARGETARCH /app/nanohub

EXPOSE 9004

VOLUME ["/app/db"]

WORKDIR /app

ENTRYPOINT ["/app/nanohub"]
