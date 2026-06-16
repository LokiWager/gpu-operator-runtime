FROM golang:1.26 AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/controller-manager ./cmd/controller-manager
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/runtime-api ./cmd/runtime-api

FROM gcr.io/distroless/static-debian12
COPY --from=builder /out/controller-manager /controller-manager
COPY --from=builder /out/runtime-api /runtime-api

EXPOSE 8080 8081 8443
ENTRYPOINT ["/controller-manager"]
