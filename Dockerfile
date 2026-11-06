# Link2Doc Go backend (API + worker in one binary). MODE=all|api|worker
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/link2doc ./cmd/link2doc

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 app
COPY --from=build /out/link2doc /usr/local/bin/link2doc
USER app
ENV PORT=8080
EXPOSE 8080
CMD ["link2doc"]
