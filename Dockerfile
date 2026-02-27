FROM golang:1.22 AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/runtime ./cmd/runtime

FROM gcr.io/distroless/static-debian12
COPY --from=builder /out/runtime /runtime

EXPOSE 8080
ENTRYPOINT ["/runtime"]
