FROM golang:1.24 AS builder

WORKDIR /app

COPY pion_bridge_test/ . 

RUN go mod tidy && go build -o main

FROM ubuntu:24.04

WORKDIR /app

COPY --from=builder /app/main .

RUN chmod +x main

ENTRYPOINT ["/app/main"]

