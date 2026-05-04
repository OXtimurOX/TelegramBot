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
	Text string `json:"text"`
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
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.WindowSize(1920, 1080),
	)

	for {
		fmt.Println("--- Цикл проверки:", time.Now().Format("15:04:05"), "---")
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
	timeCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	var homeworks []Homework
	log.Printf("[%s] Захожу на платформу...", acc.Name)

	err := chromedp.Run(timeCtx,
		chromedp.Navigate("https://pl.el-ed.ru/auth"),
		chromedp.Sleep(5*time.Second),
		chromedp.Click(`//button[contains(text(),"Понятно, согласен")]`, chromedp.BySearch, chromedp.AtLeast(0)),
		chromedp.Click(`//button[contains(., "Войти по почте")]`, chromedp.BySearch),
		chromedp.WaitVisible(`input[type="email"]`),
		chromedp.SendKeys(`input[type="email"]`, acc.Email),
		chromedp.SendKeys(`input[type="password"]`, acc.Password),
		chromedp.Click(`button[type="submit"]`),
		chromedp.Sleep(10*time.Second),
		chromedp.Navigate(acc.HomeworkURL),
		chromedp.Sleep(15*time.Second),
		// Ищем ссылки ТОЛЬКО в таблице (обычно это тег table или main область)
		// Собираем текст из соседних ячеек, чтобы точно поймать "Тип д/з"
		chromedp.Evaluate(`
   Array.from(document.querySelectorAll('tr')).map(tr => {
    const link = tr.querySelector('a');
    return {
     link: link ? link.getAttribute("href") : "",
     text: tr.innerText.replace(/\s+/g, ' ').trim()
    }
   }).filter(h => h.link !== "")
  `, &homeworks),
	)
	if err != nil {
		log.Printf("[%s] Ошибка: %v", acc.Name, err)
		return
	}

	newFound := false
	var msg []string
	for _, hw := range homeworks {
		t := strings.ToLower(hw.Text)

		// Игнорируем ссылки из бокового меню (там обычно /clan/ или /course/)
		if strings.Contains(hw.Link, "/clan/") && !strings.Contains(hw.Link, "homework") {
			continue
		}

		// Теперь фильтр жесткий: должна быть математика И (пробник ИЛИ часть ИЛИ уровень)
		// Это отсечет названия курсов в меню
		if strings.Contains(t, "математика") && (strings.Contains(t, "часть") || strings.Contains(t, "пробник")) {

			res, err := db.Exec(`INSERT INTO saved_homeworks (account, link) VALUES ($1, $2) ON CONFLICT DO NOTHING`, acc.Name, hw.Link)
			if err != nil {
				continue
			}

			aff, _ := res.RowsAffected()
			if aff > 0 {
				newFound = true
				full := hw.Link
				if !strings.HasPrefix(full, "http") {
					full = "https://pl.el-ed.ru" + full
				}

				// Вытаскиваем только название урока из длинной строки таблицы

				parts := strings.Split(hw.Text, "  ")
				title := hw.Text
				if len(parts) > 2 {
					title = parts[2]
				} // Обычно название урока в 3-й колонке

				msg = append(msg, "🔹 "+title+"\n"+full)
				log.Printf("[%s] НАШЕЛ: %s", acc.Name, title)
			}
		}
	}

	if newFound {
		sendTelegram("🔥 " + acc.Name + "\nНовые ДЗ из таблицы:\n\n" + strings.Join(msg, "\n\n"))
	} else {
		log.Printf("[%s] В таблице ничего нового", acc.Name)
	}
}

func sendTelegram(message string) {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage?chat_id=%s&text=%s", os.Getenv("TELEGRAM_BOT_TOKEN"), os.Getenv("TELEGRAM_CHAT_ID"), url.QueryEscape(message))
	http.Get(apiURL)
}
