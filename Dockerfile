# syntax=docker/dockerfile:1

FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bus-trmnl .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/bus-trmnl /app/bus-trmnl
# tzdata for the configured timezone; distroless static includes zoneinfo.
ENV TZ=America/Los_Angeles
EXPOSE 2300
VOLUME ["/data"]
ENTRYPOINT ["/app/bus-trmnl"]
CMD ["serve", "-config", "/data/config.yaml"]
