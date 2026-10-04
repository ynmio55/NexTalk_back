FROM golang:1.23-alpine AS build
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /nextalk ./cmd/server

FROM alpine:3.20
RUN adduser -D -H -u 10001 nextalk
USER nextalk
COPY --from=build /nextalk /nextalk
EXPOSE 8080
ENTRYPOINT ["/nextalk"]
