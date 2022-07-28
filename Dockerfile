FROM golang:1.18 as build
WORKDIR /usr/app
COPY go.* .
RUN go mod download
COPY main.go .
RUN CGO_ENABLED=0 GOOS=linux go build -a -o runner .

FROM python:3-alpine
WORKDIR /usr/app
COPY --from=build /usr/app/runner .
COPY requirements.txt .
RUN pip3 install -r requirements.txt
COPY docker-entrypoint.sh .
RUN chmod +x docker-entrypoint.sh
ENTRYPOINT [ "./docker-entrypoint.sh" ]
CMD [ "./runner" ]