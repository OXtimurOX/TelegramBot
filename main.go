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
	// Поднимаем веб-сервер для Railway
	go func() {
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		http.ListenAndServe(":"+port, nil)
	}()

	// Подключаемся к PostgreSQL
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("Ошибка: Переменная DATABASE_URL не задана")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal("Не удалось подключиться к БД:", err)
	}
	defer db.Close()

	// Автоматически создаем таблицу, если её еще нет
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
		}

		fmt.Println("Ждём 10 минут...")
		time.Sleep(10 * time.Minute)
	}
}

func checkAccount(acc Account, db *sql.DB) {
	// Настраиваем параметры запуска браузера специально для Railway/Docker
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.NoSandbox,                             // Обязательно для Docker
		chromedp.DisableGPU,                            // Отключаем графику
		chromedp.Headless,                              // Без видимого окна
		chromedp.Flag("disable-dev-shm-usage", true),   // КРИТИЧНО: решает проблему с памятью в Docker
		chromedp.Flag("disable-setuid-sandbox", true),
        chromedp.Flag("no-zygote", true),
        chromedp.Flag("single-process", true),
		chromedp.ExecPath("/usr/bin/chromium-browser"), // Путь к браузеру в Alpine
	)

	// Создаем аллокатор с этими опциями
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	// Создаем контекст самой вкладки браузера
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	var homeworks []Homework
	log.Println("Использую новые флаги браузера")

	err := chromedp.Run(ctx,
		chromedp.Evaluate(`Object.defineProperty(navigator, 'webdriver', {get: () => undefined})`, nil),
		chromedp.Navigate("https://pl.el-ed.ru/auth"),
		chromedp.Sleep(8*time.Second),

		chromedp.Click(`//button[contains(text(),"Понятно, согласен")]`, chromedp.BySearch),
		chromedp.Sleep(2*time.Second),

		chromedp.Click(`//button[contains(text(),"Войти по почте")]`, chromedp.BySearch),
		chromedp.SendKeys(`input[type="email"]`, acc.Email),
		chromedp.SendKeys(`input[type="password"]`, acc.Password),
		chromedp.Click(`button[type="submit"]`),

		chromedp.Sleep(5*time.Second),

		chromedp.Navigate(acc.HomeworkURL),
		chromedp.Sleep(7*time.Second),
		chromedp.Reload(),
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
		log.Println("Ошибка парсинга:", err)
		sendTelegram("❌ " + acc.Name + " — ошибка входа")
		return
	}

	fmt.Println("Найдено карточек:", len(homeworks))
	newFound := false
	var messageLines []string

	for _, hw := range homeworks {
		if strings.Contains(hw.Type, "Пробник, математика") ||
			strings.Contains(hw.Type, "Первая часть, математика") ||
			strings.Contains(hw.Type, "Первая и вторая части, математика") {

			// Пытаемся записать ДЗ в базу. Если такая ссылка уже есть, сработает DO NOTHING
			res, err := db.Exec(`INSERT INTO saved_homeworks (account, link) VALUES ($1, $2) ON CONFLICT DO NOTHING`, acc.Name, hw.Link)
			if err != nil {
				log.Println("Ошибка БД:", err)
				continue
			}
			// Проверяем, добавилась ли новая строка
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

	escaped := url.QueryEscape(message)
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage?chat_id=%s&text=%s", botToken, chatID, escaped)

	resp, err := http.Get(apiURL)
	if err != nil {
		log.Println("Ошибка Telegram:", err)
		return
	}
	defer resp.Body.Close()
}
