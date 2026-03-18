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
	go func() {
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		http.ListenAndServe(":"+port, nil)
	}()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("Ошибка: Переменная DATABASE_URL не задана")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal("Не удалось подключиться к БД:", err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS saved_homeworks (
  account VARCHAR(100),
  link TEXT,
  UNIQUE(account, link)
 );`)
	if err != nil {
		log.Fatal("Ошибка создания таблицы:", err)
	}

	accounts := []Account{
		{"matmasha.VESNA11@mail.ru", "goel2026", "Account1", "https://pl.el-ed.ru/clan/5161/homeworks"},
		{"matmasha.VESNA10@mail.ru", "goel2026", "Account2", "https://pl.el-ed.ru/clan/5164/homeworks"},
		{"matsashaVESNA11@mail.ru", "goel2026", "Account3", "https://pl.el-ed.ru/clan/5165/homeworks"},
		{"matsashaVESNA10@mail.ru", "goel2026", "Account4", "https://pl.el-ed.ru/clan/5167/homeworks"},
	}

	for {
		fmt.Println("Проверка:", time.Now().Format("15:04:05"))
		for _, acc := range accounts {
			checkAccount(acc, db)
			time.Sleep(5 * time.Second) // Даем серверу "выдохнуть" между аккаунтами
		}
		fmt.Println("Ждём 10 минут...")
		time.Sleep(10 * time.Minute)
	}
}

func checkAccount(acc Account, db *sql.DB) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		chromedp.Headless,
		chromedp.ExecPath("/usr/bin/chromium"),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("no-zygote", true),
	)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()

	// Тайм-аут на всю операцию 3 минуты, чтобы не висело вечно
	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()

	timeCtx, cancelTime := context.WithTimeout(ctx, 3*time.Minute)
	defer cancelTime()

	var homeworks []Homework
	log.Printf("[%s] Запуск браузера...", acc.Name)

	err := chromedp.Run(timeCtx,
		chromedp.Evaluate(`Object.defineProperty(navigator, 'webdriver', {get: () => undefined})`, nil),
		chromedp.Navigate("https://pl.el-ed.ru/auth"),
		chromedp.Sleep(5*time.Second),
		chromedp.Click(`//button[contains(text(),"Понятно, согласен")]`, chromedp.BySearch),
		chromedp.Sleep(5*time.Second),
		chromedp.Click(`//button[contains(text(),"Войти по почте")]`, chromedp.BySearch),
		chromedp.SendKeys(`input[type="email"]`, acc.Email),
		chromedp.SendKeys(`input[type="password"]`, acc.Password),
		chromedp.Click(`button[type="submit"]`),
		chromedp.Sleep(5*time.Second),
		chromedp.Navigate(acc.HomeworkURL),
		chromedp.Sleep(10*time.Second),
		chromedp.Evaluate(`
   Array.from(document.querySelectorAll('a[href^="/homework-done/"]')).map(a => {
    const blocks = Array.from(a.querySelectorAll('div'));
    return {
     link: a.getAttribute("href"),
     type: blocks.map(b => b.innerText).join(" ")
    }
   })
  `, &homeworks),
	)
	if err != nil {
		log.Printf("[%s] Ошибка: %v", acc.Name, err)
		return
	}

	newFound := false
	var messageLines []string
	for _, hw := range homeworks {
		if strings.Contains(hw.Type, "Пробник, математика") ||
			strings.Contains(hw.Type, "Первая часть, математика") ||
			strings.Contains(hw.Type, "Первая и вторая части, математика") {

			res, err := db.Exec(`INSERT INTO saved_homeworks (account, link) VALUES ($1, $2) ON CONFLICT DO NOTHING`, acc.Name, hw.Link)
			if err != nil {
				continue
			}

			affected, _ := res.RowsAffected()
			if affected > 0 {
				newFound = true
				messageLines = append(messageLines, "🔹 "+hw.Type+"\nhttps://pl.el-ed.ru"+hw.Link)
			}
		}
	}

	if newFound {
		sendTelegram("🔥 " + acc.Name + "\nНовые ДЗ:\n\n" + strings.Join(messageLines, "\n\n"))
	}
}

func sendTelegram(message string) {
	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatID := os.Getenv("TELEGRAM_CHAT_ID")
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage?chat_id=%s&text=%s", botToken, chatID, url.QueryEscape(message))
	resp, _ := http.Get(apiURL)
	if resp != nil {
		resp.Body.Close()
	}
}
