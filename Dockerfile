FROM golang:1.27.3 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /bin/app ./cmd/api

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /bin/app /app
# The app listens on localhost by default. A container must listen on all
# interfaces, or the published port cannot reach it. ENVIRONMENT stays
# unset, so the image runs as production.
ENV HOST=0.0.0.0
EXPOSE 8080
ENTRYPOINT ["/app"]
