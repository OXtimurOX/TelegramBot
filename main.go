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

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		chromedp.Headless,
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("no-zygote", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
	)

	for {
		fmt.Println("=== Цикл проверки:", time.Now().Format("15:04:05"), "===")
		for _, acc := range accounts {
			allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
			ctx, cancelCtx := chromedp.NewContext(allocCtx)

			checkAccount(ctx, acc, db)

			cancelCtx()
			cancelAlloc()
			time.Sleep(15 * time.Second)
		}
		fmt.Println("Пауза 10 минут...")
		time.Sleep(10 * time.Minute)
	}
}

func checkAccount(ctx context.Context, acc Account, db *sql.DB) {
	timeCtx, cancelTime := context.WithTimeout(ctx, 4*time.Minute)
	defer cancelTime()

	var homeworks []Homework
	var currentURL string

	log.Printf("[%s] Входим в аккаунт...", acc.Name)

	err := chromedp.Run(timeCtx,
		chromedp.Navigate("https://pl.el-ed.ru/auth"),
		chromedp.Sleep(5*time.Second),
		chromedp.Click(`//button[contains(text(),"Понятно, согласен")]`, chromedp.BySearch),
		// Кликаем по кнопке входа через почту (используем более точный поиск)
		chromedp.Click(`//button[contains(., "Войти по почте")]`, chromedp.BySearch),
		chromedp.WaitVisible(`input[type="email"]`, chromedp.ByQuery),
		chromedp.SendKeys(`input[type="email"]`, acc.Email),
		chromedp.SendKeys(`input[type="password"]`, acc.Password),
		chromedp.Click(`button[type="submit"]`),
		chromedp.Sleep(10*time.Second), // Ждем завершения входа

		// Переход к ДЗ
		chromedp.Navigate(acc.HomeworkURL),
		chromedp.Sleep(15*time.Second), // Даем время JS прогрузить список

		// Проверяем, где мы оказались
		chromedp.Location(&currentURL),

		// Собираем вообще ВСЕ ссылки, чтобы понять, что видит бот
		chromedp.Evaluate(`
   Array.from(document.querySelectorAll('a')).map(a => {
    return {
     link: a.getAttribute("href") || "",
     type: a.innerText.replace(/\s+/g, ' ').trim()
    }
   }).filter(item => item.link.includes("homework"))
  `, &homeworks),
	)
	if err != nil {
		log.Printf("[%s] Ошибка chromedp: %v", acc.Name, err)
		return
	}

	log.Printf("[%s] Текущий адрес: %s", acc.Name, currentURL)
	log.Printf("[%s] Найдено подходящих ссылок: %d", acc.Name, len(homeworks))

	if strings.Contains(currentURL, "/auth") {
		log.Printf("[%s] ВНИМАНИЕ: Бот не смог войти и остался на странице логина!", acc.Name)
		return
	}

	newFound := false
	var messageLines []string

	for _, hw := range homeworks {
		txt := strings.ToLower(hw.Type)
		// Если в тексте есть "математика" и ( "пробник" или "часть" )
		if strings.Contains(txt, "математика") && (strings.Contains(txt, "пробник") || strings.Contains(txt, "часть")) {

			res, err := db.Exec(`INSERT INTO saved_homeworks (account, link) VALUES ($1, $2) ON CONFLICT DO NOTHING`, acc.Name, hw.Link)
			if err != nil {
				continue
			}

			affected, _ := res.RowsAffected()
			if affected > 0 {
				newFound = true
				messageLines = append(messageLines, "🔹 "+hw.Type+"\nhttps://pl.el-ed.ru"+hw.Link)
				log.Printf("[%s] Новая работа: %s", acc.Name, hw.Type)
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
	if botToken == "" || chatID == "" {
		return
	}
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage?chat_id=%s&text=%s", botToken, chatID, url.QueryEscape(message))
	resp, _ := http.Get(apiURL)
	if resp != nil {
		resp.Body.Close()
	}
}
