FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/app .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /bin/app /app
EXPOSE 8080
ENTRYPOINT ["/app"]
