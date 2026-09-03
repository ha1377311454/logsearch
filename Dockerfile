FROM golang:1.25 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/logsearch-agent ./cmd/logsearch-agent

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/logsearch-agent /logsearch-agent
ENTRYPOINT ["/logsearch-agent"]
