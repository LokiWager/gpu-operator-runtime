FROM golang:1.26 AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/manager ./cmd/main.go

FROM gcr.io/distroless/static-debian12
COPY --from=builder /out/manager /manager

EXPOSE 8080
ENTRYPOINT ["/manager"]
