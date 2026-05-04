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
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS saved_homeworks (account VARCHAR(100), link TEXT, UNIQUE(account, link));`)

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
		chromedp.WindowSize(1920, 1080), // Увеличиваем окно
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"),
	)

	for {
		fmt.Println("--- Старт цикла:", time.Now().Format("15:04:05"), "---")
		for _, acc := range accounts {
			allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
			ctx, cancelCtx := chromedp.NewContext(allocCtx)
			checkAccount(ctx, acc, db)
			cancelCtx()
			cancelAlloc()
			time.Sleep(10 * time.Second)
		}
		time.Sleep(10 * time.Minute)
	}
}

func checkAccount(ctx context.Context, acc Account, db *sql.DB) {
	timeCtx, cancelTime := context.WithTimeout(ctx, 5*time.Minute)
	defer cancelTime()

	var homeworks []Homework
	var pageText string

	log.Printf("[%s] Начинаю процесс...", acc.Name)

	err := chromedp.Run(timeCtx,
		chromedp.Navigate("https://pl.el-ed.ru/auth"),
		chromedp.Sleep(5*time.Second),
		chromedp.Click(`//button[contains(text(),"Понятно, согласен")]`, chromedp.BySearch, chromedp.AtLeast(0)),
		chromedp.Click(`//button[contains(., "Войти по почте")]`, chromedp.BySearch),
		chromedp.WaitVisible(`input[type="email"]`),
		chromedp.SendKeys(`input[type="email"]`, acc.Email),
		chromedp.SendKeys(`input[type="password"]`, acc.Password),
		chromedp.Click(`button[type="submit"]`),
		chromedp.Sleep(15*time.Second), // Ждем подольше после входа

		chromedp.Navigate(acc.HomeworkURL),
		chromedp.Sleep(20*time.Second), // Очень долго ждем загрузки списка ДЗ

		// Берем ВЕСЬ текст страницы для отладки
		chromedp.Text("body", &pageText),

		// Собираем все ссылки, где есть "homework" или цифры ID в конце
		chromedp.Evaluate(`
   Array.from(document.querySelectorAll('a')).map(a => ({
    link: a.getAttribute("href") || "",
    type: a.innerText.trim()
   })).filter(h => h.link.includes("homework") || /\d+$/.test(h.link))
  `, &homeworks),
	)
	if err != nil {
		log.Printf("[%s] Ошибка: %v", acc.Name, err)
		return
	}

	// Если ссылок 0, выведем в лог первые 500 символов текста страницы
	if len(homeworks) == 0 {
		snippet := pageText
		if len(snippet) > 500 {
			snippet = snippet[:500]
		}
		log.Printf("[%s] Ссылок не нашли. Текст на странице (кусок): %s", acc.Name, snippet)
	}

	newFound := false
	var msg []string
	for _, hw := range homeworks {
		t := strings.ToLower(hw.Type)
		// Упростим фильтр: если есть "мат" и "часть" или "проб"
		if strings.Contains(t, "мат") && (strings.Contains(t, "час") || strings.Contains(t, "проб")) {
			res, _ := db.Exec(`INSERT INTO saved_homeworks (account, link) VALUES ($1, $2) ON CONFLICT DO NOTHING`, acc.Name, hw.Link)
			aff, _ := res.RowsAffected()
			if aff > 0 {
				newFound = true
				fullLink := hw.Link
				if !strings.
					HasPrefix(fullLink, "http") {
					fullLink = "https://pl.el-ed.ru" + hw.Link
				}
				msg = append(msg, "🔹 "+hw.Type+"\n"+fullLink)
			}
		}
	}

	if newFound {
		sendTelegram("🔥 " + acc.Name + "\nНовые ДЗ:\n\n" + strings.Join(msg, "\n\n"))
	}
}

func sendTelegram(message string) {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage?chat_id=%s&text=%s", os.Getenv("TELEGRAM_BOT_TOKEN"), os.Getenv("TELEGRAM_CHAT_ID"), url.QueryEscape(message))
	http.Get(apiURL)
}
