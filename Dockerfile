# syntax=docker/dockerfile:1

FROM golang:1.18-alpine

WORKDIR /rarity

COPY go.mod go.sum ./
RUN go mod download

COPY ./ ./

RUN CGO_ENABLED=0 GOOS=linux go build -o /polymorphs-rarity-v1

CMD [ "/polymorphs-rarity-v1" ]

