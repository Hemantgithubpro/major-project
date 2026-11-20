FROM golang:1.23-alpine AS b
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY shared ./shared
COPY apps/safety-shield ./apps/safety-shield
RUN go build -o /app ./apps/safety-shield
FROM alpine:3.20
COPY --from=b /app /app
CMD ["/app"]
