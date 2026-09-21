FROM golang:1.23-alpine AS b
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY shared ./shared
COPY apps/gateway ./apps/gateway
RUN go build -o /app ./apps/gateway
FROM alpine:3.20
COPY --from=b /app /app
CMD ["/app"]
