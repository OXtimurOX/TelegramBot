FROM golang:1.24-alpine AS builder

# Устанавливаем зависимости для сборки
RUN apk add --no-cache git

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o main .

# Финальный образ с Chrome
FROM alpine:latest
RUN apk add --no-cache \
    chromium \
    chromium-chromedriver \
    harfbuzz \
    nss \
    freetype \
    ttf-freefont

WORKDIR /app
COPY --from=builder /app/main .

# Указываем путь к Chrome для библиотеки chromedp
ENV CHROME_BIN=/usr/bin/chromium-browser

CMD ["./main"]