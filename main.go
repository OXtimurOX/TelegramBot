package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	_ "github.com/lib/pq"
)

type Account struct {
	Email       string
	Password    string
	Name        string
	HomeworkURL string
}

type Homework struct {
	Link string `json:"link"`
	Type string `json:"type"`
}

func main() {
	// чтобы Railway не убивал сервис
	go func() {
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		http.ListenAndServe(":"+port, nil)
	}()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is missing")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`
 CREATE TABLE IF NOT EXISTS saved_homeworks (
  account VARCHAR(100),
  link TEXT,
  UNIQUE(account, link)
 );`)
	if err != nil {
		log.Fatal(err)
	}

	accounts := []Account{
		{"matmasha.VESNA11@mail.ru", "goel2026", "Account1", "https://pl.el-ed.ru/clan/5161/homeworks"},
		{"matmasha.VESNA10@mail.ru", "goel2026", "Account2", "https://pl.el-ed.ru/clan/5164/homeworks"},
		{"matsashaVESNA11@mail.ru", "goel2026", "Account3", "https://pl.el-ed.ru/clan/5165/homeworks"},
		{"matsashaVESNA10@mail.ru", "goel2026", "Account4", "https://pl.el-ed.ru/clan/5167/homeworks"},
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		chromedp.Headless,
		chromedp.ExecPath("/usr/bin/chromium"),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("no-zygote", true),
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.WindowSize(1920, 1080),
	)

	for {
		fmt.Println("=== Старт цикла:", time.Now().Format("15:04:05"), "===")

		for _, acc := range accounts {

			allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
			ctx, cancelCtx := chromedp.NewContext(allocCtx)

			checkAccount(ctx, acc, db)

			cancelCtx()
			cancelAlloc()

			time.Sleep(15 * time.Second) // важно
		}

		fmt.Println("⏸ Пауза 10 минут...")
		time.Sleep(10 * time.Minute)
	}
}

func checkAccount(ctx context.Context, acc Account, db *sql.DB) {
	timeCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	var homeworks []Homework
	var currentURL string
	var html string

	log.Printf("[%s] Входим...", acc.Name)

	err := chromedp.Run(timeCtx,
		chromedp.Navigate("https://pl.el-ed.ru/auth"),
		chromedp.Sleep(5*time.Second),

		chromedp.Click(`//button[contains(text(),"Понятно, согласен")]`, chromedp.BySearch, chromedp.AtLeast(0)),
		chromedp.Sleep(2*time.Second),

		chromedp.Click(`//button[contains(., "Войти по почте")]`, chromedp.BySearch),
		chromedp.WaitVisible(`input[type="email"]`),

		chromedp.SendKeys(`input[type="email"]`, acc.Email),
		chromedp.SendKeys(`input[type="password"]`, acc.Password),
		chromedp.Click(`button[type="submit"]`),

		chromedp.Sleep(10*time.Second),

		chromedp.Navigate(acc.HomeworkURL),

		// ВАЖНО: ждём реальные элементы
		chromedp.WaitVisible(`a[href*="homework"]`, chromedp.ByQuery),
		chromedp.Sleep(5*time.Second),

		chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight)`, nil),
		chromedp.Sleep(3*time.Second),

		chromedp.Location(&currentURL),
		chromedp.OuterHTML("html", &html),

		chromedp.Evaluate(`
Array.from(document.querySelectorAll('a')).map(a => {
 return {
  link: a.getAttribute("href") || "",
  type: a.innerText.replace(/\s+/g, ' ').trim()
 }
}).filter(h => 
 h.link.includes("/homework-done/") || 
 h.link.includes("/homework/")
)
  `, &homeworks),
	)
	if err != nil {
		log.Printf("[%s] Ошибка: %v", acc.Name, err)
		return
	}

	log.Printf("[%s] URL: %s | Найдено: %d | HTML size: %d",
		acc.Name, currentURL, len(homeworks), len(html))

	newFound := false
	var msg []string

	for _, hw := range homeworks {

		log.Printf("[%s] Вижу: %s | %s", acc.Name, hw.Type, hw.Link)

		t := strings.ToLower(hw.Type)
		if strings.Contains(t, "математика") &&
			(strings.Contains(t, "пробник") || strings.Contains(t, "часть")) {

			res, err := db.Exec(`
INSERT INTO saved_homeworks (account, link)
VALUES ($1, $2)
ON CONFLICT DO NOTHING
   `, acc.Name, hw.Link)
			if err != nil {
				log.Printf("[%s] DB ошибка: %v", acc.Name, err)
				continue
			}

			aff, _ := res.RowsAffected()

			if aff > 0 {
				newFound = true

				full := hw.Link
				if !strings.HasPrefix(full, "http") {
					full = "https://pl.el-ed.ru" + full
				}

				msg = append(msg, "🔹 "+hw.Type+"\n"+full)
			}
		}
	}

	if newFound {
		sendTelegram("🔥 " + acc.Name + "\nНовые ДЗ:\n\n" + strings.Join(msg, "\n\n"))
	} else {
		log.Printf("[%s] Новых ДЗ нет", acc.Name)
	}
}

func sendTelegram(message string) {
	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatID := os.Getenv("TELEGRAM_CHAT_ID")

	if botToken == "" || chatID == "" {
		log.Println("Telegram env пустые")
		return
	}

	apiURL := fmt.Sprintf(
		"https://api.telegram.org/bot%s/sendMessage?chat_id=%s&text=%s",
		botToken,
		chatID,
		url.QueryEscape(message),
	)

	resp, err := http.Get(apiURL)
	if err != nil {
		log.Println("Telegram error:", err)
		return
	}
	defer resp.Body.Close()
}
