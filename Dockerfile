## Build stage
FROM golang:1-alpine AS builder

WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o github_status .

## Final stage — distroless for minimal attack surface
FROM gcr.io/distroless/static:nonroot

COPY --from=builder /app/github_status /github_status

EXPOSE 8080
ENTRYPOINT ["/github_status"]
