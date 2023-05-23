FROM golang:alpine as builder

WORKDIR /var/app/src

COPY go.* ./

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o /dist/app .

FROM scratch

COPY --from=builder /dist/app /app

ENTRYPOINT [ "/app" ]