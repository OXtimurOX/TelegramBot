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
	if dbURL == "" {
		log.Fatal("DATABASE_URL is missing")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS saved_homeworks (account VARCHAR(100), link TEXT, UNIQUE(account, link));`)

	accounts := []Account{
		{"6probnikm@mail.ru", "goelprobe", "Account1", "https://pl.el-ed.ru/clan/5294/homeworks"},
		{"7probnikm@mail.ru", "goelprobe", "Account2", "https://pl.el-ed.ru/clan/5293/homeworks"},
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
		fmt.Println("=== Старт проверки:", time.Now().Format("15:04:05"), "===")
		for _, acc := range accounts {
			allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
			ctx, cancelCtx := chromedp.NewContext(allocCtx)
			checkAccount(ctx, acc, db)
			cancelCtx()
			cancelAlloc()
			time.Sleep(10 * time.Second)
		}
		fmt.Println("⏸️ Ждем 10 минут...")
		time.Sleep(10 * time.Minute)
	}
}

func checkAccount(ctx context.Context, acc Account, db *sql.DB) {
	timeCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var homeworks []Homework
	log.Printf("[%s] Вхожу...", acc.Name)

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
		chromedp.Sleep(20*time.Second), // Даем таблице прогрузиться

		// Скроллим вообще всё, чтобы таблица точно загрузилась
		chromedp.ActionFunc(func(ctx context.Context) error {
			for i := 0; i < 5; i++ {
				chromedp.Evaluate(`
     window.scrollBy(0, 1000);
     document.querySelectorAll('*').forEach(el => {
      if (el.scrollHeight > el.clientHeight) el.scrollTop += 1000;
     });
    `, nil).Do(ctx)
				time.Sleep(2 * time.Second)
			}
			return nil
		}),

		// А ТЕПЕРЬ ГЛАВНОЕ: берем ТЕКСТ из ВСЕХ строк таблицы (tr), игнорируя наличие ссылок
		chromedp.Evaluate(`
   Array.from(document.querySelectorAll('tr')).map(tr => {
    let a = tr.querySelector('a');
    return {
     link: a ? a.getAttribute("href") : "",
     text: tr.innerText.replace(/\s+/g, ' ').trim()
    }
   }).filter(h => h.text.length > 10)
  `, &homeworks),
	)
	if err != nil {
		log.Printf("[%s] Ошибка: %v", acc.Name, err)
		return
	}

	newFound := false
	var msg []string

	for _, hw := range homeworks {
		txt := strings.ToLower(hw.Text)

		// Жёсткий фильтр: ищем твои слова прямо в тексте строки
		isMath := strings.Contains(txt, "математика") || strings.Contains(txt, "мат")
		isProbnik := strings.Contains(txt, "пробник") || strings.Contains(txt, "проб")
		isPart := strings.Contains(txt, "часть") || strings.Contains(txt, "части")

		// Ищем совпадение
		if isMath && (isProbnik || isPart) {

			// Поскольку реальной ссылки может не быть (из-за JS-кликов),
			// мы используем текст самой строки (первые 100 символов) как уникальный ID для базы данных!
			dbKey := acc.Name + "_" + hw.Text
			if len(dbKey) > 100 {
				dbKey = dbKey[:100]
			}

			res, err := db.Exec(`INSERT INTO saved_homeworks (account, link) VALUES ($1, $2) ON CONFLICT DO NOTHING`, acc.Name, dbKey)
			if err != nil {
				continue
			}

			aff, _ := res.RowsAffected()
			if aff > 0 {
				newFound = true

				// Формируем ссылку: если ее нет, даем ссылку на общую страницу ДЗ
				finalLink := hw.Link
				if finalLink == "" || finalLink == "#" || strings.Contains(finalLink, "javascript") {
					finalLink = acc.HomeworkURL
				} else if !strings.HasPrefix(finalLink, "http") {
					finalLink = "https://pl.el-ed.ru" + finalLink
				}

				// Собираем сообщение
				msg = append(msg, "🔹 Найдена работа:\n"+hw.Text+"\n\nСтраница: "+finalLink)
				log.Printf("[%s] БИНГО! Найдено: %s", acc.Name, dbKey)
			}
		}
	}

	if newFound {
		sendTelegram("🔥 " + acc.Name + "\nНовые работы:\n\n" + strings.Join(msg, "\n\n---\n"))
	} else {
		log.Printf("[%s] В таблице ничего нового по фильтру не найдено", acc.Name)
	}
}

func sendTelegram(message string) {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage?chat_id=%s&text=%s", os.Getenv("TELEGRAM_BOT_TOKEN"), os.Getenv("TELEGRAM_CHAT_ID"), url.QueryEscape(message))
	http.Get(apiURL)
}
