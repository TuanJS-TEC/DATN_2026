FROM golang:1-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -o /server ./cmd/server

FROM alpine:3.20
# ca-certificates: server calls SSI/CafeF over https (internal/httpx.Fetch)
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /server ./server
COPY web ./web
# Render/Heroku override PORT at `docker run` time; Fly reads fly.toml's internal_port instead.
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["./server"]
