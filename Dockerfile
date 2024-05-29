# Dockerfile
FROM golang:1.19-alpine

WORKDIR /app

# COPY go.mod ./
# COPY go.sum ./
RUN go mod init load_balancer

COPY *.go ./

RUN go build -o /load_balancer

EXPOSE 8080

CMD ["/load_balancer"]
