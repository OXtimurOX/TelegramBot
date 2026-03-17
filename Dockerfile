FROM golang:1.24-alpine AS builder

RUN apt-get update && apt-get install -y \
    chromium \
    chromium-driver \
    fonts-liberation \
    libnss3 \
    libatk-bridge2.0-0 \
    libgtk-3-0 \
    libx11-xcb1 \
    libxcomposite1 \
    libxdamage1 \
    libxrandr2 \
    xdg-utils

WORKDIR /app

COPY . .

RUN go mod init bot || true
RUN go mod tidy

RUN go build -o bot .

CMD ["./bot"]