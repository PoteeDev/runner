FROM golang:1.18 as build
WORKDIR /usr/app
COPY go.* .
RUN go mod download
COPY main.go .
RUN CGO_ENABLED=0 GOOS=linux go build -a -o runner .

FROM alpine
WORKDIR /usr/app
COPY --from=build /usr/app/runner .
ENTRYPOINT [ "./runner" ]