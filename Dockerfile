FROM golang:1.24.2-alpine AS builder

# environment variables
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64

WORKDIR /workspace

COPY go.mod go.sum ./

RUN go mod tidy
RUN go mod download

COPY . .

# building the KubeBalance controller binary
RUN go build -o manager cmd/manager/main.go

FROM alpine/git:latest as git
FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

COPY --from=builder /workspace/manager /manager

ENTRYPOINT ["/manager"]
